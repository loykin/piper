package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// Validation errors name the JSON field ("max_runs"), not the Go struct field
// ("MaxRuns"). Set once for gin's shared validator when the package loads.
func init() {
	if engine, ok := binding.Validator.Engine().(*validator.Validate); ok {
		engine.RegisterTagNameFunc(func(f reflect.StructField) string {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				return f.Name
			}
			return name
		})
	}
}

// BindJSON decodes the request body into v and reports whether it succeeded.
// On failure it writes a 400 whose message names the JSON field ("type is
// required", "max_runs has the wrong type") instead of the validator's Go-side
// text ("Key: 'openRequest.Type' Error:Field validation for 'Type' failed
// on the 'required' tag"), which is what users used to see in form errors.
func BindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": bindErrorMessage(err)})
		return false
	}
	return true
}

func bindErrorMessage(err error) string {
	var fieldErrs validator.ValidationErrors
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &fieldErrs):
		msgs := make([]string, 0, len(fieldErrs))
		for _, fe := range fieldErrs {
			switch fe.Tag() {
			case "required":
				msgs = append(msgs, fe.Field()+" is required")
			case "oneof":
				msgs = append(msgs, fmt.Sprintf("%s must be one of: %s", fe.Field(), strings.ReplaceAll(fe.Param(), " ", ", ")))
			default:
				msgs = append(msgs, fmt.Sprintf("%s is invalid (%s)", fe.Field(), fe.Tag()))
			}
		}
		return strings.Join(msgs, "; ")
	case errors.As(err, &typeErr):
		return fmt.Sprintf("%s has the wrong type (expected %s)", typeErr.Field, typeErr.Type.Kind())
	case errors.As(err, &syntaxErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "request body must be valid JSON"
	default:
		return "invalid request body: " + err.Error()
	}
}
