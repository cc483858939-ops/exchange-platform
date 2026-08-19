package models

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestUserRecoProfileUsesNullableVectorFields(t *testing.T) {
	parsed, err := schema.Parse(&UserRecoProfile{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"positive_vector", "negative_vector"} {
		field, ok := parsed.FieldsByDBName[name]
		if !ok {
			t.Fatalf("missing %s field", name)
		}
		if field.FieldType.Kind() != reflect.Ptr {
			t.Fatalf("%s field type=%s, want pointer for SQL NULL", name, field.FieldType.Kind())
		}
		if field.DataType != "vector" && field.GORMDataType != "vector" {
			t.Fatalf("%s data type=%q gorm=%q, want vector", name, field.DataType, field.GORMDataType)
		}
	}
}
