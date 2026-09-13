package mpvhost

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gen2brain/go-mpv"
)

// FormatArg renders one element of a JSON command array the way mpv's
// input.conf-style command parser expects it. JSON numbers arrive as float64,
// so integers must lose their ".0" or mpv rejects them for integer arguments.
func FormatArg(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		if x {
			return "yes", nil
		}
		return "no", nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("non-finite number %v", x)
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case float32:
		return FormatArg(float64(x))
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case json99:
		return x.String(), nil
	default:
		return "", fmt.Errorf("unsupported command argument %T", v)
	}
}

// json99 lets callers pass encoding/json.Number without importing it here.
type json99 interface{ String() string }

// CommandStrings converts a whole JSON command array to mpv's string form.
func CommandStrings(cmd []any) ([]string, error) {
	out := make([]string, 0, len(cmd))
	for i, v := range cmd {
		s, err := FormatArg(v)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// propertyFormat picks the mpv format for a JSON value being written to a
// property. Integral numbers go in as int64 so choice/int properties such as
// "sid" or "chapter" accept them.
func propertyFormat(v any) (mpv.Format, any, bool) {
	switch x := v.(type) {
	case bool:
		return mpv.FormatFlag, x, true
	case string:
		return mpv.FormatString, x, true
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return mpv.FormatInt64, int64(x), true
		}
		return mpv.FormatDouble, x, true
	case int:
		return mpv.FormatInt64, int64(x), true
	case int64:
		return mpv.FormatInt64, x, true
	case nil:
		return mpv.FormatString, "", true
	default:
		return mpv.FormatNone, nil, false
	}
}

// IsVirtual reports whether a command is handled by the projector itself
// rather than being forwarded to mpv.
func IsVirtual(cmd []any) bool {
	if len(cmd) == 0 {
		return false
	}
	s, ok := cmd[0].(string)
	return ok && strings.HasPrefix(s, "vs/")
}

// Command runs a JSON-IPC style command array against the embedded mpv.
//
// libmpv's C API has no "set_property"/"get_property" command (those exist only
// in the JSON IPC protocol), so those two names are translated to the property
// API; everything else is passed through as an input.conf-style string array.
func (h *Host) Command(cmd []any) (any, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	name, _ := cmd[0].(string)
	switch name {
	case "set_property", "set-property":
		if len(cmd) != 3 {
			return nil, fmt.Errorf("set_property needs a name and a value")
		}
		prop, ok := cmd[1].(string)
		if !ok {
			return nil, fmt.Errorf("set_property: property name must be a string")
		}
		format, val, ok := propertyFormat(cmd[2])
		if !ok {
			return nil, fmt.Errorf("set_property: unsupported value %T", cmd[2])
		}
		if err := h.mpv.SetProperty(prop, format, val); err != nil {
			// mpv is strict about formats for some properties; its string
			// parser accepts everything the JSON side can express.
			s, ferr := FormatArg(cmd[2])
			if ferr != nil {
				return nil, err
			}
			if err2 := h.mpv.SetPropertyString(prop, s); err2 != nil {
				return nil, err
			}
		}
		return nil, nil

	case "get_property", "get-property":
		if len(cmd) != 2 {
			return nil, fmt.Errorf("get_property needs a property name")
		}
		prop, ok := cmd[1].(string)
		if !ok {
			return nil, fmt.Errorf("get_property: property name must be a string")
		}
		return h.mpv.GetProperty(prop, mpv.FormatNode)

	case "get_property_string":
		if len(cmd) != 2 {
			return nil, fmt.Errorf("get_property_string needs a property name")
		}
		prop, _ := cmd[1].(string)
		return h.mpv.GetPropertyString(prop), nil
	}

	args, err := CommandStrings(cmd)
	if err != nil {
		return nil, err
	}
	return h.mpv.CommandRet(args)
}
