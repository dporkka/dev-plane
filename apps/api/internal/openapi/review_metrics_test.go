package openapi

import "testing"

func TestReviewMetricsDocumented(t *testing.T) {
	spec := BuildSpec()
	route, ok := spec.Paths["/api/v1/organizations/{orgID}/review-metrics"]
	if !ok || route.Get == nil {
		t.Fatal("review metrics GET endpoint absent from OpenAPI")
	}
	if route.Get.OperationID != "getReviewMetrics" {
		t.Fatalf("unexpected operation ID: %s", route.Get.OperationID)
	}
	if len(route.Get.Security) == 0 {
		t.Fatal("review metrics must require authentication")
	}
	if len(route.Get.Parameters) != 4 {
		t.Fatalf("want path + 3 query params, got %d", len(route.Get.Parameters))
	}
	schema := spec.Components.Schemas["ReviewMetrics"]
	if schema == nil || schema.Properties["reviewed_prs"] == nil || schema.Properties["security_findings"] == nil {
		t.Fatal("review metrics schema incomplete")
	}
}
