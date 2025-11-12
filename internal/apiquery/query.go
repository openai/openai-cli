package apiquery

import (
	"net/url"
	"reflect"
)

func MarshalWithSettings(value any, settings QuerySettings) (url.Values, error) {
	val := reflect.ValueOf(value)
	if !val.IsValid() {
		return nil, nil
	}

	e := encoder{settings}
	pairs, err := e.Encode("", val)
	if err != nil {
		return nil, err
	}

	kv := url.Values{}
	for _, pair := range pairs {
		kv.Add(pair.key, pair.value)
	}
	return kv, nil
}
func Marshal(value any) (url.Values, error) {
	return MarshalWithSettings(value, QuerySettings{})
}

type Queryer interface {
	URLQuery() (url.Values, error)
}

type NestedQueryFormat int

const (
	NestedQueryFormatBrackets NestedQueryFormat = iota
	NestedQueryFormatDots
)

type ArrayQueryFormat int

const (
	ArrayQueryFormatComma ArrayQueryFormat = iota
	ArrayQueryFormatRepeat
	ArrayQueryFormatIndices
	ArrayQueryFormatBrackets
)

type QuerySettings struct {
	NestedFormat NestedQueryFormat
	ArrayFormat  ArrayQueryFormat
}
