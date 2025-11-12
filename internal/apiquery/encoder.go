package apiquery

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type encoder struct {
	settings QuerySettings
}

type Pair struct {
	key   string
	value string
}

func (e *encoder) Encode(key string, value reflect.Value) ([]Pair, error) {
	t := value.Type()
	switch t.Kind() {
	case reflect.Pointer:
		if value.IsNil() || !value.IsValid() {
			return []Pair{{key, ""}}, nil
		}
		return e.Encode(key, value.Elem())

	case reflect.Array, reflect.Slice:
		return e.encodeArray(key, value)

	case reflect.Map:
		return e.encodeMap(key, value)

	case reflect.Interface:
		if !value.Elem().IsValid() {
			return []Pair{{key, ""}}, nil
		}
		return e.Encode(key, value.Elem())

	default:
		return e.encodePrimitive(key, value)
	}
}

func (e *encoder) encodeMap(key string, value reflect.Value) ([]Pair, error) {
	var pairs []Pair
	iter := value.MapRange()
	for iter.Next() {
		subkey := iter.Key().String()
		keyPath := subkey
		if len(key) > 0 {
			if e.settings.NestedFormat == NestedQueryFormatDots {
				keyPath = fmt.Sprintf("%s.%s", key, subkey)
			} else {
				keyPath = fmt.Sprintf("%s[%s]", key, subkey)
			}
		}

		subpairs, err := e.Encode(keyPath, iter.Value())
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, subpairs...)
	}
	return pairs, nil
}

func (e *encoder) encodeArray(key string, value reflect.Value) ([]Pair, error) {
	switch e.settings.ArrayFormat {
	case ArrayQueryFormatComma:
		elements := []string{}
		for i := 0; i < value.Len(); i++ {
			innerPairs, err := e.Encode("", value.Index(i))
			if err != nil {
				return nil, err
			}
			for _, pair := range innerPairs {
				elements = append(elements, pair.value)
			}
		}
		return []Pair{{key, strings.Join(elements, ",")}}, nil

	case ArrayQueryFormatRepeat:
		var pairs []Pair
		for i := 0; i < value.Len(); i++ {
			subpairs, err := e.Encode(key, value.Index(i))
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, subpairs...)
		}
		return pairs, nil

	case ArrayQueryFormatIndices:
		var pairs []Pair
		for i := 0; i < value.Len(); i++ {
			subpairs, err := e.Encode(fmt.Sprintf("%s[%d]", key, i), value.Index(i))
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, subpairs...)
		}
		return pairs, nil

	case ArrayQueryFormatBrackets:
		var pairs []Pair
		for i := 0; i < value.Len(); i++ {
			subpairs, err := e.Encode(key+"[]", value.Index(i))
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, subpairs...)
		}
		return pairs, nil

	default:
		panic(fmt.Sprintf("Unknown ArrayFormat value: %d", e.settings.ArrayFormat))
	}
}

func (e *encoder) encodePrimitive(key string, value reflect.Value) ([]Pair, error) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsValid() || value.IsNil() {
			return nil, nil
		}
		return e.encodePrimitive(key, value.Elem())

	case reflect.String:
		return []Pair{{key, value.String()}}, nil

	case reflect.Bool:
		if value.Bool() {
			return []Pair{{key, "true"}}, nil
		}
		return []Pair{{key, "false"}}, nil

	case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int64:
		return []Pair{{key, strconv.FormatInt(value.Int(), 10)}}, nil

	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return []Pair{{key, strconv.FormatUint(value.Uint(), 10)}}, nil

	case reflect.Float32, reflect.Float64:
		return []Pair{{key, strconv.FormatFloat(value.Float(), 'f', -1, 64)}}, nil

	default:
		return nil, nil
	}
}

func (e *encoder) encodeField(key string, value reflect.Value) ([]Pair, error) {
	present := value.FieldByName("Present")
	if !present.Bool() {
		return nil, nil
	}
	null := value.FieldByName("Null")
	if null.Bool() {
		return nil, fmt.Errorf("apiquery: field cannot be null")
	}
	raw := value.FieldByName("Raw")
	if !raw.IsNil() {
		return e.Encode(key, raw)
	}
	return e.Encode(key, value.FieldByName("Value"))
}
