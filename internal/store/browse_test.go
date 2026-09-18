package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func stringPointer(value string) *string { return &value }

func TestBuildBrowseQuery(t *testing.T) {
	t.Parallel()
	columns := []BrowseColumn{{Name: "id"}, {Name: "display`name"}, {Name: "notes"}}
	req := BrowseRequest{
		Schema: "app`data", Table: "users`archive", Offset: 10, PageSize: 25,
		Sort: &BrowseSort{Column: "display`name", Direction: "desc"},
		Filters: []BrowseFilter{
			{Column: "id", Operator: "equals", Value: stringPointer("7")},
			{Column: "display`name", Operator: "contains", Value: stringPointer("%x_")},
			{Column: "notes", Operator: "isNull"},
		},
	}

	query, args, err := buildBrowseQuery(req, columns)
	if err != nil {
		t.Fatal(err)
	}
	wantQuery := "SELECT `id`, `display``name`, `notes` FROM `app``data`.`users``archive` WHERE `id` = ? AND LOCATE(?, `display``name`) > 0 AND `notes` IS NULL ORDER BY `display``name` DESC LIMIT ? OFFSET ?"
	if query != wantQuery {
		t.Fatalf("query = %q\nwant    %q", query, wantQuery)
	}
	wantArgs := []any{"7", "%x_", 26, 10}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestBuildBrowseQueryRejectsUntrustedFragments(t *testing.T) {
	t.Parallel()
	columns := []BrowseColumn{{Name: "id"}, {Name: "name"}}
	tests := []struct {
		name string
		req  BrowseRequest
	}{
		{name: "unknown filter column", req: BrowseRequest{PageSize: 10, Filters: []BrowseFilter{{Column: "missing", Operator: "isNull"}}}},
		{name: "operator fragment", req: BrowseRequest{PageSize: 10, Filters: []BrowseFilter{{Column: "id", Operator: "equals OR 1=1", Value: stringPointer("1")}}}},
		{name: "missing value", req: BrowseRequest{PageSize: 10, Filters: []BrowseFilter{{Column: "name", Operator: "contains"}}}},
		{name: "value on null operator", req: BrowseRequest{PageSize: 10, Filters: []BrowseFilter{{Column: "name", Operator: "isNull", Value: stringPointer("x")}}}},
		{name: "unknown sort column", req: BrowseRequest{PageSize: 10, Sort: &BrowseSort{Column: "missing", Direction: "asc"}}},
		{name: "direction fragment", req: BrowseRequest{PageSize: 10, Sort: &BrowseSort{Column: "id", Direction: "asc, name"}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := buildBrowseQuery(test.req, columns)
			if !errors.Is(err, ErrInvalidBrowse) {
				t.Fatalf("error = %v, want ErrInvalidBrowse", err)
			}
		})
	}
}

func TestNormalizeBrowseRequest(t *testing.T) {
	t.Parallel()
	req := BrowseRequest{Schema: "app", Table: "customers"}
	if err := normalizeBrowseRequest(&req); err != nil {
		t.Fatal(err)
	}
	if req.PageSize != 100 {
		t.Fatalf("default page size = %d, want 100", req.PageSize)
	}

	invalid := []BrowseRequest{
		{Table: "customers", PageSize: 10},
		{Schema: "app", PageSize: 10},
		{Schema: "app", Table: "customers", Offset: -1, PageSize: 10},
		{Schema: "app", Table: "customers", PageSize: 201},
		{Schema: "app", Table: "customers", PageSize: 10, Filters: make([]BrowseFilter, 21)},
	}
	for _, value := range invalid {
		err := normalizeBrowseRequest(&value)
		if !errors.Is(err, ErrInvalidBrowse) || !strings.Contains(err.Error(), "invalid browse request") {
			t.Fatalf("normalizeBrowseRequest(%+v) error = %v", value, err)
		}
	}
}
