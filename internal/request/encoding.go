package request

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

type BodyContentType int

const (
	EmptyBody BodyContentType = iota
	MultipartFormEncoded
	ApplicationJSON
	ApplicationOctetStream
)

// EncodingHooks retains feature validation and observation at their existing
// serialization points without making shared request code depend on features.
type EncodingHooks struct {
	PrepareMultipart func(map[string]any) error
	ObserveMultipart func(io.Closer)
	ValidateJSON     func([]byte) error
	ObserveJSON      []func([]byte)
}

// EncodeOptions serializes already-expanded query, header, and body values.
// Explicit literal headers are appended by the caller after these options.
// On success, a multipart body owns its uploads. Until then, the caller retains
// responsibility for closing uploads, including failures from feature hooks.
func EncodeOptions(
	cmd *cli.Command,
	contents requestflag.RequestContents,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,
	hooks EncodingHooks,
) (options []option.RequestOption, err error) {
	querySettings := apiquery.QuerySettings{
		NestedFormat: nestedFormat,
		ArrayFormat:  arrayFormat,
	}

	// Add query parameters:
	if values, err := apiquery.MarshalWithSettings(contents.Queries, querySettings); err != nil {
		return nil, err
	} else {
		for k, vs := range values {
			if len(vs) == 0 {
				options = append(options, option.WithQueryDel(k))
			} else {
				options = append(options, option.WithQuery(k, vs[0]))
				for _, v := range vs[1:] {
					options = append(options, option.WithQueryAdd(k, v))
				}
			}
		}
	}

	// Add header parameters
	headerSettings := apiquery.QuerySettings{
		NestedFormat: apiquery.NestedQueryFormatDots,
		ArrayFormat:  apiquery.ArrayQueryFormatRepeat,
	}
	if values, err := apiquery.MarshalWithSettings(contents.Headers, headerSettings); err != nil {
		return nil, err
	} else {
		for k, vs := range values {
			if len(vs) == 0 {
				options = append(options, option.WithHeaderDel(k))
			} else {
				options = append(options, option.WithHeader(k, vs[0]))
				for _, v := range vs[1:] {
					options = append(options, option.WithHeaderAdd(k, v))
				}
			}
		}
	}

	switch bodyType {
	case EmptyBody:
		break
	case MultipartFormEncoded:
		// For multipart/form-encoded, we need a map structure
		bodyMap, ok := contents.Body.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Cannot send a non-map value to a form-encoded endpoint: %v\n", contents.Body)
		}
		if hooks.PrepareMultipart != nil {
			if err := hooks.PrepareMultipart(bodyMap); err != nil {
				return nil, err
			}
		}
		// Multipart dispatch follows actual Booleans without consuming uploads
		// or interpreting strings that happen to resemble scalar values.
		if streaming, ok := bodyMap["stream"].(bool); ok {
			if err := ResolveStreamingValue(cmd, streaming); err != nil {
				return nil, err
			}
		}
		encodingFormat := apiform.FormatBrackets
		var observers []func(io.Closer)
		if hooks.ObserveMultipart != nil {
			observers = append(observers, hooks.ObserveMultipart)
		}
		multipartOptions, err := MultipartRequestOptions(bodyMap, encodingFormat, observers...)
		if err != nil {
			return nil, err
		}
		options = append(options, multipartOptions...)

	case ApplicationJSON:
		bodyBytes, err := json.Marshal(contents.Body)
		if err != nil {
			return nil, err
		}
		if hooks.ValidateJSON != nil {
			if err := hooks.ValidateJSON(bodyBytes); err != nil {
				return nil, err
			}
		}
		if err := ResolveStreamingInput(cmd, bodyBytes); err != nil {
			return nil, err
		}
		options = append(options, option.WithRequestBody("application/json", bodyBytes))
		for _, observe := range hooks.ObserveJSON {
			observe(bodyBytes)
		}

	case ApplicationOctetStream:
		// If there is a body root parameter, that will handle setting the request body, we don't need to do it here.
		for _, flag := range cmd.Flags {
			if toSend, ok := flag.(requestflag.InRequest); ok && toSend.IsBodyRoot() {
				return options, nil
			}
		}
		if bodyBytes, ok := contents.Body.([]byte); ok {
			options = append(options, option.WithRequestBody("application/octet-stream", bodyBytes))
		} else if bodyStr, ok := contents.Body.(string); ok {
			options = append(options, option.WithRequestBody("application/octet-stream", []byte(bodyStr)))
		} else {
			return nil, fmt.Errorf("Unsupported body for application/octet-stream: %v", contents.Body)
		}

	default:
		panic("Invalid body content type!")
	}

	return options, nil
}
