package openapi

import "testing"

func TestBrowserJobsRoutesDocumentedWithJWTAndStates(t *testing.T) {
	spec := BuildSpec()
	for _, tc := range []struct{ path, method string }{
		{"/api/v1/organizations/{orgID}/browser-jobs", "POST"},
		{"/api/v1/organizations/{orgID}/browser-jobs/{id}", "GET"},
	} {
		item, ok := spec.Paths[tc.path]
		if !ok {
			t.Errorf("OpenAPI missing %s", tc.path)
			continue
		}
		var operation *Operation
		if tc.method == "POST" {
			operation = item.Post
		} else {
			operation = item.Get
		}
		if operation == nil {
			t.Errorf("OpenAPI missing %s %s", tc.method, tc.path)
			continue
		}
		if len(operation.Security) == 0 {
			t.Errorf("%s %s missing JWT requirement", tc.method, tc.path)
		}
	}
}
