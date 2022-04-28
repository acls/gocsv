package gocsv

import (
	"fmt"
	"reflect"
)

type Encoder struct {
	writer     CSVWriter
	inType     reflect.Type
	wasPointer bool
	structInfo *structInfo
	row        []string
}

func NewEncoder(writer CSVWriter, in interface{}) (*Encoder, error) {
	_, inType := getConcreteReflectValueAndType(in) // Get the concrete type (not pointer)
	if err := ensureInInnerType(inType); err != nil {
		return nil, err
	}
	structInfo := getStructInfo(inType) // Get the struct info to get CSV annotations
	return &Encoder{
		writer:     writer,
		inType:     inType,
		wasPointer: inType.Kind() == reflect.Ptr,
		structInfo: structInfo,
		row:        make([]string, len(structInfo.Fields)),
	}, nil
}

func (e *Encoder) WriteHeader() error {
	for i, fieldInfo := range e.structInfo.Fields { // Used to write the header (first line) in CSV
		e.row[i] = fieldInfo.getFirstKey()
	}
	return e.writer.Write(e.row)
}
func (e *Encoder) Encode(in interface{}) error {
	val, inType := getConcreteReflectValueAndType(in) // Get the concrete type (not pointer)
	if e.inType != inType {
		return fmt.Errorf("Encoder was initialized to encode %v, but received %v", e.inType, inType)
	}
	for j, fieldInfo := range e.structInfo.Fields {
		e.row[j] = ""
		inInnerFieldValue, err := getInnerField(val, e.wasPointer, fieldInfo.IndexChain) // Get the correct field header <-> position
		if err != nil {
			return err
		}
		e.row[j] = inInnerFieldValue
	}
	e.writer.Write(e.row)
	e.writer.Flush()
	return e.writer.Error()
}

func writeFromChan(writer CSVWriter, c <-chan interface{}, omitHeaders bool) error {
	// Get the first value. It wil determine the header structure.
	firstValue, ok := <-c
	if !ok {
		return fmt.Errorf("channel is closed")
	}
	inValue, inType := getConcreteReflectValueAndType(firstValue) // Get the concrete type
	if err := ensureStructOrPtr(inType); err != nil {
		return err
	}
	inInnerWasPointer := inType.Kind() == reflect.Ptr
	inInnerStructInfo := getStructInfo(inType) // Get the inner struct info to get CSV annotations
	csvHeadersLabels := make([]string, len(inInnerStructInfo.Fields))
	for i, fieldInfo := range inInnerStructInfo.Fields { // Used to write the header (first line) in CSV
		csvHeadersLabels[i] = fieldInfo.getFirstKey()
	}
	if !omitHeaders {
		if err := writer.Write(csvHeadersLabels); err != nil {
			return err
		}
	}
	write := func(val reflect.Value) error {
		for j, fieldInfo := range inInnerStructInfo.Fields {
			csvHeadersLabels[j] = ""
			inInnerFieldValue, err := getInnerField(val, inInnerWasPointer, fieldInfo.IndexChain) // Get the correct field header <-> position
			if err != nil {
				return err
			}
			csvHeadersLabels[j] = inInnerFieldValue
		}
		if err := writer.Write(csvHeadersLabels); err != nil {
			return err
		}
		return nil
	}
	if err := write(inValue); err != nil {
		return err
	}
	for v := range c {
		val, _ := getConcreteReflectValueAndType(v) // Get the concrete type (not pointer) (Slice<?> or Array<?>)
		if err := ensureStructOrPtr(inType); err != nil {
			return err
		}
		if err := write(val); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func writeTo(writer CSVWriter, in interface{}, omitHeaders bool) error {
	inValue, inType := getConcreteReflectValueAndType(in) // Get the concrete type (not pointer) (Slice<?> or Array<?>)
	if err := ensureInType(inType); err != nil {
		return err
	}
	inInnerWasPointer, inInnerType := getConcreteContainerInnerType(inType) // Get the concrete inner type (not pointer) (Container<"?">)
	if err := ensureInInnerType(inInnerType); err != nil {
		return err
	}
	inInnerStructInfo := getStructInfo(inInnerType) // Get the inner struct info to get CSV annotations
	csvHeadersLabels := make([]string, len(inInnerStructInfo.Fields))
	for i, fieldInfo := range inInnerStructInfo.Fields { // Used to write the header (first line) in CSV
		csvHeadersLabels[i] = fieldInfo.getFirstKey()
	}
	if !omitHeaders {
		if err := writer.Write(csvHeadersLabels); err != nil {
			return err
		}
	}
	inLen := inValue.Len()
	for i := 0; i < inLen; i++ { // Iterate over container rows
		for j, fieldInfo := range inInnerStructInfo.Fields {
			csvHeadersLabels[j] = ""
			inInnerFieldValue, err := getInnerField(inValue.Index(i), inInnerWasPointer, fieldInfo.IndexChain) // Get the correct field header <-> position
			if err != nil {
				return err
			}
			csvHeadersLabels[j] = inInnerFieldValue
		}
		if err := writer.Write(csvHeadersLabels); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func ensureStructOrPtr(t reflect.Type) error {
	switch t.Kind() {
	case reflect.Struct:
		fallthrough
	case reflect.Ptr:
		return nil
	}
	return fmt.Errorf("cannot use " + t.String() + ", only slice or array supported")
}

// Check if the inType is an array or a slice
func ensureInType(outType reflect.Type) error {
	switch outType.Kind() {
	case reflect.Slice:
		fallthrough
	case reflect.Array:
		return nil
	}
	return fmt.Errorf("cannot use " + outType.String() + ", only slice or array supported")
}

// Check if the inInnerType is of type struct
func ensureInInnerType(outInnerType reflect.Type) error {
	switch outInnerType.Kind() {
	case reflect.Struct:
		return nil
	}
	return fmt.Errorf("cannot use " + outInnerType.String() + ", only struct supported")
}

func getInnerField(outInner reflect.Value, outInnerWasPointer bool, index []int) (string, error) {
	oi := outInner
	if outInnerWasPointer {
		if oi.IsNil() {
			return "", nil
		}
		oi = outInner.Elem()
	}

	if oi.Kind() == reflect.Slice || oi.Kind() == reflect.Array {
		i := index[0]

		if i >= oi.Len() {
			return "", nil
		}

		item := oi.Index(i)
		if len(index) > 1 {
			return getInnerField(item, false, index[1:])
		}
		return getFieldAsString(item)
	}

	// because pointers can be nil need to recurse one index at a time and perform nil check
	if len(index) > 1 {
		nextField := oi.Field(index[0])
		return getInnerField(nextField, nextField.Kind() == reflect.Ptr, index[1:])
	}
	return getFieldAsString(oi.FieldByIndex(index))
}
