package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rcrespodev/neugram/internal/store"
)

func failWith(f *fixture, err error) (int, errorBody) {
	rec := httptest.NewRecorder()
	f.srv.fail(rec, httptest.NewRequest("GET", "/x", nil), err)
	var e errorBody
	json.NewDecoder(rec.Body).Decode(&e)
	return rec.Code, e
}

func TestEveryCatalogCodeReturnsItsStatus(t *testing.T) {
	f := newFixture(t)
	want := map[string]int{
		"no_pin": 422, "unknown_task": 404, "task_exists": 409, "invalid_parent": 422,
		"unknown_product": 404, "product_exists": 409, "unknown_session": 404, "not_found": 404,
		"invalid_request": 400, "invalid_type": 422, "invalid_enum": 422, "required_field": 422,
		"invalid_field": 422, "invalid_report": 422, "topic_key_collision": 409, "field_exists": 409,
		"reserved_name": 422, "field_in_use": 409, "import_precheck_failed": 422,
	}
	if len(errorCatalog) != len(want)+1 {
		t.Fatalf("catalog has %d codes, want %d", len(errorCatalog), len(want)+1)
	}
	for code, status := range want {
		got, e := failWith(f, &store.Error{Code: code, Message: "m"})
		if got != status || e.Error.Code != code || e.Error.Hint == "" {
			t.Errorf("%s: status %d body %+v", code, got, e.Error)
		}
	}
}

func TestNoPinCarriesOpenTasks(t *testing.T) {
	f := newFixture(t)
	_, e := failWith(f, &store.Error{Code: "no_pin", Message: "m"})
	if len(e.Error.Details.OpenTasks) == 0 {
		t.Fatalf("details = %+v", e.Error.Details)
	}
}

func TestUncataloguedErrorsAreInternal(t *testing.T) {
	f := newFixture(t)
	for _, err := range []error{errors.New("boom"), &store.Error{Code: "mystery"}} {
		status, e := failWith(f, err)
		if status != http.StatusInternalServerError || e.Error.Code != "internal" {
			t.Errorf("%v: status %d code %s", err, status, e.Error.Code)
		}
	}
}
