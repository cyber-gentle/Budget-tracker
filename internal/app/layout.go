package app

import "reflect"

// layoutFields are the page-data fields the shared layout templates read.
var layoutFields = []string{"Username", "FullName", "FirstName", "Email"}

// layoutData adapts any page's template data (struct or map) for the shared
// layout templates in layout.html, adding which page is active:
//
//	{{template "app-header" (layout "goals" .)}}
func layoutData(active string, data any) map[string]any {
	out := map[string]any{"Active": active}
	for _, name := range layoutFields {
		out[name] = fieldValue(data, name)
	}
	return out
}

// fieldValue reads a named field from a struct or string-keyed map, or "" if absent.
func fieldValue(data any, name string) any {
	v := reflect.ValueOf(data)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() == reflect.String {
			if mv := v.MapIndex(reflect.ValueOf(name).Convert(v.Type().Key())); mv.IsValid() && mv.CanInterface() {
				return mv.Interface()
			}
		}
	case reflect.Struct:
		if f := v.FieldByName(name); f.IsValid() && f.CanInterface() {
			return f.Interface()
		}
	}
	return ""
}
