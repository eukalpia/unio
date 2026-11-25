package meta

import (
	"reflect"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/object"
)

// fill sets every field of the struct v points to a distinct non-zero value,
// so that a field Info forgets to copy is visible.
func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(v.Type().Field(i).Name + "-value")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(1000 + i))
		case reflect.Map:
			f.Set(reflect.ValueOf(map[string]string{"color": "blue"}))
		case reflect.Struct:
			switch f.Interface().(type) {
			case time.Time:
				f.Set(reflect.ValueOf(time.Unix(1_700_000_000, 0).UTC()))
			case object.Checksum:
				f.Set(reflect.ValueOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Type: object.ChecksumComposite, Value: []byte{1, 2, 3, 4}}))
			default:
				t.Fatalf("field %s: add a value for %s", v.Type().Field(i).Name, f.Type())
			}
		case reflect.Slice:
			if f.Type().Elem().Kind() == reflect.Uint8 {
				f.SetBytes([]byte("inline"))
			} else {
				f.Set(reflect.MakeSlice(f.Type(), 3, 3))
			}
		default:
			t.Fatalf("field %s: unhandled kind %s", v.Type().Field(i).Name, f.Kind())
		}
	}
}

// TestObjectRecordInfo checks that Info copies every client-visible field of
// the record. A field missing here would be stored but silently dropped from
// HEAD, GET and LIST responses, including the checksum clients verify.
func TestObjectRecordInfo(t *testing.T) {
	var rec ObjectRecord
	fill(t, reflect.ValueOf(&rec).Elem())

	info := rec.Info()
	iv := reflect.ValueOf(info)
	rv := reflect.ValueOf(rec)
	derived := map[string]bool{"IsLatest": true, "Parts": true}
	for i := 0; i < iv.NumField(); i++ {
		name := iv.Type().Field(i).Name
		if derived[name] {
			continue
		}
		src := rv.FieldByName(name)
		if !src.IsValid() {
			t.Errorf("object.Info.%s has no source in ObjectRecord", name)
			continue
		}
		if !reflect.DeepEqual(iv.Field(i).Interface(), src.Interface()) {
			t.Errorf("Info().%s = %v, want %v", name, iv.Field(i).Interface(), src.Interface())
		}
	}
	if info.IsLatest {
		t.Error("Info sets IsLatest; only callers that know the version is current may")
	}
	if info.Parts != len(rec.Parts) {
		t.Errorf("Info().Parts = %d for a multipart object with %d parts", info.Parts, len(rec.Parts))
	}

	rec.Multipart = false
	if got := rec.Info().Parts; got != 0 {
		t.Errorf("Info().Parts = %d for a single-part object, want 0", got)
	}
}
