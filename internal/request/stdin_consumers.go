package request

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
)

// StdinConsumers counts the values that file embedding consumes, without reading them.
// Count effective values so a replaced scalar flag does not claim stdin twice.
func StdinConsumers(contents requestflag.RequestContents, reserved bool) (int, error) {
	count := stdinConsumers(reflect.ValueOf(contents.Body)) +
		stdinConsumers(reflect.ValueOf(contents.Queries)) +
		stdinConsumers(reflect.ValueOf(contents.Headers))
	if reserved {
		count++
	}
	if count > 1 {
		return count, fmt.Errorf("multiple request parameters use stdin; select only one stdin consumer")
	}
	return count, nil
}

func stdinConsumers(value reflect.Value) int {
	return stdinInputConsumers(value, false)
}

func stdinInputConsumers(value reflect.Value, structured bool) int {
	if !value.IsValid() {
		return 0
	}
	if value.Kind() == reflect.Interface || value.Type() == reflect.TypeFor[*string]() {
		if value.IsNil() {
			return 0
		}
		return stdinInputConsumers(value.Elem(), structured)
	}
	if value.Type() == reflect.TypeFor[requestflag.InputValue]() {
		input := value.Interface().(requestflag.InputValue)
		return input.CountSources(func(value any, expandSources bool) int {
			return stdinInputConsumers(reflect.ValueOf(value), !expandSources)
		})
	}
	switch value.Kind() {
	case reflect.Map:
		count := 0
		iter := value.MapRange()
		for iter.Next() {
			count += stdinInputConsumers(iter.Value(), structured)
		}
		return count
	case reflect.Array, reflect.Slice:
		count := 0
		for i := 0; i < value.Len(); i++ {
			count += stdinInputConsumers(value.Index(i), structured)
		}
		return count
	case reflect.String:
		if structured || value.Type() == reflect.TypeFor[UntrustedStdinValue]() {
			return 0
		}
		path := value.String()
		if value.Type() != reflect.TypeFor[FilePathValue]() {
			var reference bool
			path, reference = strings.CutPrefix(path, "@")
			if !reference {
				return 0
			}
			if rest, ok := strings.CutPrefix(path, "file://"); ok {
				path = rest
			} else if rest, ok := strings.CutPrefix(path, "data://"); ok {
				path = rest
			}
		}
		if IsStdinPath(path) {
			return 1
		}
	}
	return 0
}
