package openapi

import "testing"

func TestPRTriageEndpointsInOpenAPI(t *testing.T) {
	spec := BuildSpec()
	list, ok := spec.Paths["/api/v1/organizations/{orgID}/pr-triage"]
	if !ok || list.Get == nil || list.Get.OperationID != "listExternalPRTriage" {
		t.Fatal("missing authenticated triage listing documentation")
	}
	if len(list.Get.Security) == 0 || len(list.Get.Parameters) < 2 {
		t.Fatal("list route missing authentication or filters")
	}
	importer, ok := spec.Paths["/api/v1/organizations/{orgID}/pr-triage/import"]
	if !ok || importer.Post == nil || importer.Post.OperationID != "importExternalPRTriage" {
		t.Fatal("missing triage import documentation")
	}
	if len(importer.Post.Security) == 0 || importer.Post.RequestBody == nil {
		t.Fatal("import missing authentication or request body")
	}
}
