// Package openapi generates and serves the OpenAPI specification for the API.
//
// The spec is built programmatically to match the domain models and handlers
// exactly, ensuring the documentation stays in sync with the implementation.
package openapi

import (
	"encoding/json"
	"fmt"
)

const (
	openAPIVersion = "3.0.3"
	apiTitle       = "AI Dev Control Plane API"
	apiDescription = `API for managing AI-driven software development tasks,
agent runs, approvals, policies, and integrations.

Authentication is via JWT Bearer tokens obtained through GitHub OAuth.
All timestamps are returned in ISO 8601 format (UTC).`
	apiVersion = "1.0.0"
)

// Spec is the root OpenAPI document.
type Spec struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Servers    []Server            `json:"servers"`
	Paths      map[string]PathItem `json:"paths"`
	Components Components          `json:"components"`
	Tags       []Tag               `json:"tags,omitempty"`
}

// Info provides metadata about the API.
type Info struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Contact     *Contact `json:"contact,omitempty"`
}

// Contact information for the API.
type Contact struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Server represents an API server.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// PathItem describes operations available on a single path.
type PathItem struct {
	Get    *Operation `json:"get,omitempty"`
	Post   *Operation `json:"post,omitempty"`
	Put    *Operation `json:"put,omitempty"`
	Patch  *Operation `json:"patch,omitempty"`
	Delete *Operation `json:"delete,omitempty"`
}

// Operation describes a single API operation.
type Operation struct {
	Tags        []string              `json:"tags,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	OperationID string                `json:"operationId,omitempty"`
	Parameters  []Parameter           `json:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses"`
	Security    []SecurityRequirement `json:"security,omitempty"`
}

// Parameter describes an operation parameter.
type Parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"` // query, header, path, cookie
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
}

// RequestBody describes a request body.
type RequestBody struct {
	Description string               `json:"description,omitempty"`
	Required    bool                 `json:"required,omitempty"`
	Content     map[string]MediaType `json:"content"`
}

// Response describes a single response.
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// MediaType provides schema and examples for the media type.
type MediaType struct {
	Schema  *Schema                `json:"schema,omitempty"`
	Example map[string]interface{} `json:"example,omitempty"`
}

// Schema describes the data type.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Format      string             `json:"format,omitempty"`
	Description string             `json:"description,omitempty"`
	Nullable    bool               `json:"nullable,omitempty"`
	Ref         string             `json:"$ref,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Enum        []interface{}      `json:"enum,omitempty"`
	Example     interface{}        `json:"example,omitempty"`
}

// Components holds reusable schemas, parameters, responses, etc.
type Components struct {
	Schemas         map[string]*Schema        `json:"schemas"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes"`
}

// SecurityScheme defines a security scheme.
type SecurityScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
	Description  string `json:"description,omitempty"`
}

// SecurityRequirement specifies which security schemes are required.
type SecurityRequirement map[string][]string

// Tag adds metadata to a group of operations.
type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// BuildSpec constructs the complete OpenAPI specification.
func BuildSpec() *Spec {
	spec := &Spec{
		OpenAPI: openAPIVersion,
		Info: Info{
			Title:       apiTitle,
			Description: apiDescription,
			Version:     apiVersion,
			Contact: &Contact{
				Name: "AI Dev Control Plane Team",
				URL:  "https://github.com/ai-dev-control-plane",
			},
		},
		Servers: []Server{
			{URL: "http://localhost:8080", Description: "Local development server"},
			{URL: "/", Description: "Current server"},
		},
		Paths:      buildPaths(),
		Components: buildComponents(),
		Tags: []Tag{
			{Name: "Auth", Description: "Authentication endpoints"},
			{Name: "Organizations", Description: "Organization management"},
			{Name: "Projects", Description: "Project management"},
			{Name: "Repositories", Description: "Repository connections"},
			{Name: "Tasks", Description: "Task lifecycle management"},
			{Name: "Agent Runs", Description: "Agent execution management"},
			{Name: "Approvals", Description: "Human approval workflows"},
			{Name: "Policies", Description: "Policy and RBAC management"},
			{Name: "Integrations", Description: "Third-party integrations"},
			{Name: "Audit Logs", Description: "Audit trail"},
			{Name: "Dashboard", Description: "Dashboard and analytics"},
			{Name: "Health", Description: "Health checks"},
			{Name: "Pull Requests", Description: "Pull request management"},
			{Name: "Workspaces", Description: "Workspace and runtime management"},
			{Name: "Artifacts", Description: "Artifact retrieval"},
			{Name: "Secrets", Description: "Encrypted secret management"},
			{Name: "Webhooks", Description: "Incoming webhook providers"},
		},
	}

	return spec
}

// buildComponents defines all reusable schemas matching domain models.
func buildComponents() Components {
	return Components{
		Schemas: map[string]*Schema{
			"Organization": {
				Type:     "object",
				Required: []string{"id", "name", "slug", "plan", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":         {Type: "string", Format: "uuid"},
					"name":       {Type: "string"},
					"slug":       {Type: "string"},
					"plan":       {Type: "string", Enum: []interface{}{"free", "pro", "enterprise"}},
					"settings":   {Type: "object", Nullable: true},
					"created_at": {Type: "string", Format: "date-time"},
					"updated_at": {Type: "string", Format: "date-time"},
					"deleted_at": {Type: "string", Format: "date-time", Nullable: true},
				},
			},
			"Project": {
				Type:     "object",
				Required: []string{"id", "organization_id", "name", "slug", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":              {Type: "string", Format: "uuid"},
					"organization_id": {Type: "string", Format: "uuid"},
					"name":            {Type: "string"},
					"slug":            {Type: "string"},
					"description":     {Type: "string", Nullable: true},
					"settings":        {Type: "object", Nullable: true},
					"created_at":      {Type: "string", Format: "date-time"},
					"updated_at":      {Type: "string", Format: "date-time"},
					"deleted_at":      {Type: "string", Format: "date-time", Nullable: true},
				},
			},
			"Repository": {
				Type:     "object",
				Required: []string{"id", "project_id", "owner", "name", "full_name", "clone_url", "default_branch", "connection_status", "created_at"},
				Properties: map[string]*Schema{
					"id":                {Type: "string", Format: "uuid"},
					"project_id":        {Type: "string", Format: "uuid"},
					"github_id":         {Type: "integer", Nullable: true},
					"owner":             {Type: "string"},
					"name":              {Type: "string"},
					"full_name":         {Type: "string"},
					"clone_url":         {Type: "string", Format: "uri"},
					"default_branch":    {Type: "string"},
					"private":           {Type: "boolean"},
					"connection_status": {Type: "string", Enum: []interface{}{"pending", "connected", "error"}},
					"last_synced_at":    {Type: "string", Format: "date-time", Nullable: true},
					"settings":          {Type: "object", Nullable: true},
					"created_at":        {Type: "string", Format: "date-time"},
					"updated_at":        {Type: "string", Format: "date-time"},
					"deleted_at":        {Type: "string", Format: "date-time", Nullable: true},
				},
			},
			"Task": {
				Type:     "object",
				Required: []string{"id", "project_id", "repository_id", "created_by", "source", "title", "status", "priority", "risk_level", "target_branch", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":                    {Type: "string", Format: "uuid"},
					"project_id":            {Type: "string", Format: "uuid"},
					"repository_id":         {Type: "string", Format: "uuid"},
					"workspace_id":          {Type: "string", Format: "uuid", Nullable: true},
					"created_by":            {Type: "string"},
					"source":                {Type: "string", Enum: []interface{}{"web", "github_issue", "linear", "slack", "discord", "webhook", "voice"}},
					"source_id":             {Type: "string", Nullable: true},
					"title":                 {Type: "string"},
					"description":           {Type: "string", Nullable: true},
					"status":                {Type: "string", Enum: []interface{}{"backlog", "spec_review", "approved", "running", "reviewing", "pr_created", "deploying", "done", "failed", "cancelled"}},
					"priority":              {Type: "string", Enum: []interface{}{"low", "medium", "high", "urgent"}},
					"risk_level":            {Type: "string", Enum: []interface{}{"low", "medium", "high", "critical"}},
					"target_branch":         {Type: "string"},
					"spec":                  {Type: "object", Nullable: true},
					"acceptance_criteria":   {Type: "object", Nullable: true},
					"max_cost":              {Type: "number", Nullable: true},
					"max_runtime_minutes":   {Type: "integer"},
					"approval_requirements": {Type: "object", Nullable: true},
					"metadata":              {Type: "object", Nullable: true},
					"started_at":            {Type: "string", Format: "date-time", Nullable: true},
					"completed_at":          {Type: "string", Format: "date-time", Nullable: true},
					"created_at":            {Type: "string", Format: "date-time"},
					"updated_at":            {Type: "string", Format: "date-time"},
					"deleted_at":            {Type: "string", Format: "date-time", Nullable: true},
				},
			},
			"TaskSpec": {
				Type:     "object",
				Required: []string{"id", "task_id", "summary", "problem_statement", "implementation_plan", "files_to_change", "files_to_create", "acceptance_criteria", "test_plan", "risk_assessment", "rollback_plan", "required_approvals", "estimated_cost", "recommended_agent", "generated_by", "generated_at"},
				Properties: map[string]*Schema{
					"id":                  {Type: "string", Format: "uuid"},
					"task_id":             {Type: "string", Format: "uuid"},
					"summary":             {Type: "string"},
					"problem_statement":   {Type: "string"},
					"implementation_plan": {Type: "array", Items: &Schema{Type: "string"}},
					"files_to_change":     {Type: "array", Items: &Schema{Type: "string"}},
					"files_to_create":     {Type: "array", Items: &Schema{Type: "string"}},
					"acceptance_criteria": {Type: "array", Items: &Schema{Type: "string"}},
					"test_plan":           {Type: "string"},
					"risk_assessment":     {Type: "string"},
					"rollback_plan":       {Type: "string"},
					"required_approvals":  {Type: "array", Items: &Schema{Type: "string"}},
					"estimated_cost":      {Type: "number"},
					"recommended_agent":   {Type: "string"},
					"generated_by":        {Type: "string"},
					"generated_at":        {Type: "string", Format: "date-time"},
				},
			},
			"AgentRun": {
				Type:     "object",
				Required: []string{"id", "task_id", "agent_role", "status", "prompt_tokens", "completion_tokens", "total_cost", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":                {Type: "string", Format: "uuid"},
					"task_id":           {Type: "string", Format: "uuid"},
					"workspace_id":      {Type: "string", Format: "uuid", Nullable: true},
					"agent_role":        {Type: "string", Enum: []interface{}{"planner", "implementer", "reviewer", "test_runner", "security_reviewer", "docs_writer", "release_manager"}},
					"model":             {Type: "string", Nullable: true},
					"provider":          {Type: "string", Nullable: true},
					"status":            {Type: "string", Enum: []interface{}{"pending", "queued", "running", "paused", "completed", "failed", "cancelled"}},
					"started_at":        {Type: "string", Format: "date-time", Nullable: true},
					"completed_at":      {Type: "string", Format: "date-time", Nullable: true},
					"prompt_tokens":     {Type: "integer"},
					"completion_tokens": {Type: "integer"},
					"total_cost":        {Type: "number"},
					"error_message":     {Type: "string", Nullable: true},
					"summary":           {Type: "string", Nullable: true},
					"metadata":          {Type: "object", Nullable: true},
					"created_at":        {Type: "string", Format: "date-time"},
					"updated_at":        {Type: "string", Format: "date-time"},
				},
			},
			"AgentStep": {
				Type:     "object",
				Required: []string{"id", "agent_run_id", "step_number", "step_type", "status", "cost", "latency_ms", "created_at"},
				Properties: map[string]*Schema{
					"id":             {Type: "string", Format: "uuid"},
					"agent_run_id":   {Type: "string", Format: "uuid"},
					"step_number":    {Type: "integer"},
					"step_type":      {Type: "string", Enum: []interface{}{"thought", "tool_call", "command_run", "file_patch", "approval_request", "message", "error"}},
					"status":         {Type: "string", Enum: []interface{}{"pending", "running", "completed", "failed"}},
					"content":        {Type: "string", Nullable: true},
					"tool_name":      {Type: "string", Nullable: true},
					"tool_input":     {Type: "object", Nullable: true},
					"tool_output":    {Type: "object", Nullable: true},
					"command":        {Type: "string", Nullable: true},
					"command_output": {Type: "string", Nullable: true},
					"exit_code":      {Type: "integer", Nullable: true},
					"file_path":      {Type: "string", Nullable: true},
					"diff":           {Type: "string", Nullable: true},
					"cost":           {Type: "number"},
					"latency_ms":     {Type: "integer"},
					"created_at":     {Type: "string", Format: "date-time"},
				},
			},
			"Approval": {
				Type:     "object",
				Required: []string{"id", "task_id", "approval_type", "requested_by", "requested_at", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":            {Type: "string", Format: "uuid"},
					"task_id":       {Type: "string", Format: "uuid"},
					"agent_run_id":  {Type: "string", Format: "uuid", Nullable: true},
					"approval_type": {Type: "string", Enum: []interface{}{"spec", "execution", "pr_create", "deploy", "risky_action"}},
					"requested_by":  {Type: "string"},
					"requested_at":  {Type: "string", Format: "date-time"},
					"responded_by":  {Type: "string", Nullable: true},
					"response":      {Type: "string", Nullable: true, Enum: []interface{}{"approved", "rejected"}},
					"response_note": {Type: "string", Nullable: true},
					"responded_at":  {Type: "string", Format: "date-time", Nullable: true},
					"expires_at":    {Type: "string", Format: "date-time", Nullable: true},
					"metadata":      {Type: "object", Nullable: true},
					"created_at":    {Type: "string", Format: "date-time"},
					"updated_at":    {Type: "string", Format: "date-time"},
				},
			},
			"Policy": {
				Type:     "object",
				Required: []string{"id", "organization_id", "name", "resource_type", "action", "effect", "priority", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":              {Type: "string", Format: "uuid"},
					"organization_id": {Type: "string", Format: "uuid"},
					"project_id":      {Type: "string", Format: "uuid", Nullable: true},
					"name":            {Type: "string"},
					"resource_type":   {Type: "string", Enum: []interface{}{"file", "command", "secret", "deploy", "git", "network"}},
					"action":          {Type: "string", Enum: []interface{}{"read", "write", "execute", "delete"}},
					"effect":          {Type: "string", Enum: []interface{}{"allow", "ask", "deny", "admin_only"}},
					"conditions":      {Type: "object", Nullable: true},
					"priority":        {Type: "integer"},
					"created_at":      {Type: "string", Format: "date-time"},
					"updated_at":      {Type: "string", Format: "date-time"},
				},
			},
			"Integration": {
				Type:     "object",
				Required: []string{"id", "organization_id", "integration_type", "display_name", "status", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":               {Type: "string", Format: "uuid"},
					"organization_id":  {Type: "string", Format: "uuid"},
					"integration_type": {Type: "string", Enum: []interface{}{"github", "linear", "slack", "discord", "webhook", "voice"}},
					"display_name":     {Type: "string"},
					"config":           {Type: "object", Nullable: true},
					"status":           {Type: "string", Enum: []interface{}{"pending", "connected", "error", "disconnected"}},
					"webhook_url":      {Type: "string", Nullable: true},
					"last_synced_at":   {Type: "string", Format: "date-time", Nullable: true},
					"provider":         {Ref: "#/components/schemas/IntegrationProvider", Nullable: true},
					"created_at":       {Type: "string", Format: "date-time"},
					"updated_at":       {Type: "string", Format: "date-time"},
					"deleted_at":       {Type: "string", Format: "date-time", Nullable: true},
				},
			},
			"IntegrationProvider": {
				Type:     "object",
				Required: []string{"type", "name", "description", "capabilities", "required_config_fields", "supports_webhook", "supports_commands", "supports_voice"},
				Properties: map[string]*Schema{
					"type":                   {Type: "string", Enum: []interface{}{"github", "linear", "slack", "discord", "webhook", "voice"}},
					"name":                   {Type: "string"},
					"description":            {Type: "string"},
					"capabilities":           {Type: "array", Items: &Schema{Type: "string"}},
					"required_config_fields": {Type: "array", Items: &Schema{Type: "string"}},
					"supports_webhook":       {Type: "boolean"},
					"supports_commands":      {Type: "boolean"},
					"supports_voice":         {Type: "boolean"},
				},
			},
			"CreateIntegrationRequest": {
				Type:     "object",
				Required: []string{"integration_type", "display_name"},
				Properties: map[string]*Schema{
					"integration_type": {Type: "string", Enum: []interface{}{"github", "linear", "slack", "discord", "webhook", "voice"}},
					"display_name":     {Type: "string"},
					"config":           {Type: "object", Nullable: true},
					"token":            {Type: "string", Nullable: true},
					"webhook_url":      {Type: "string", Format: "uri", Nullable: true},
				},
			},
			"UpdateIntegrationRequest": {
				Type: "object",
				Properties: map[string]*Schema{
					"display_name": {Type: "string", Nullable: true},
					"config":       {Type: "object", Nullable: true},
					"status":       {Type: "string", Enum: []interface{}{"pending", "connected", "error", "disconnected"}, Nullable: true},
					"token":        {Type: "string", Nullable: true},
					"webhook_url":  {Type: "string", Format: "uri", Nullable: true},
				},
			},
			"IntegrationVerifyResponse": {
				Type:     "object",
				Required: []string{"valid", "status"},
				Properties: map[string]*Schema{
					"valid":  {Type: "boolean"},
					"status": {Type: "string", Enum: []interface{}{"pending", "connected", "error", "disconnected"}},
					"error":  {Type: "string", Nullable: true},
				},
			},
			"CreateVoiceTaskRequest": {
				Type:     "object",
				Required: []string{"repository_id", "transcript"},
				Properties: map[string]*Schema{
					"repository_id": {Type: "string", Format: "uuid"},
					"title":         {Type: "string", Nullable: true},
					"transcript":    {Type: "string"},
					"provider":      {Type: "string", Nullable: true},
					"metadata":      {Type: "object", Nullable: true},
				},
			},
			"Workspace": {
				Type:     "object",
				Required: []string{"id", "repository_id", "name", "branch", "base_branch", "runtime_provider", "status", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":                 {Type: "string", Format: "uuid"},
					"repository_id":      {Type: "string", Format: "uuid"},
					"task_id":            {Type: "string", Format: "uuid", Nullable: true},
					"name":               {Type: "string"},
					"branch":             {Type: "string"},
					"base_branch":        {Type: "string"},
					"worktree_path":      {Type: "string", Nullable: true},
					"runtime_provider":   {Type: "string"},
					"runtime_session_id": {Type: "string", Nullable: true},
					"status":             {Type: "string", Enum: []interface{}{"pending", "preparing", "ready", "running", "stopped", "error", "destroyed"}},
					"preview_url":        {Type: "string", Nullable: true},
					"settings":           {Type: "object", Nullable: true},
					"created_at":         {Type: "string", Format: "date-time"},
					"updated_at":         {Type: "string", Format: "date-time"},
					"deleted_at":         {Type: "string", Format: "date-time", Nullable: true},
				},
			},
			"DashboardData": {
				Type: "object",
				Properties: map[string]*Schema{
					"stats":        {Ref: "#/components/schemas/DashboardStats"},
					"active_runs":  {Type: "array", Items: &Schema{Ref: "#/components/schemas/AgentRun"}},
					"recent_tasks": {Type: "array", Items: &Schema{Ref: "#/components/schemas/Task"}},
				},
			},
			"DashboardStats": {
				Type: "object",
				Properties: map[string]*Schema{
					"active_runs":       {Type: "integer"},
					"tasks_today":       {Type: "integer"},
					"cost_today":        {Type: "number"},
					"pending_approvals": {Type: "integer"},
				},
			},
			"ApiError": {
				Type: "object",
				Properties: map[string]*Schema{
					"error":   {Type: "string"},
					"message": {Type: "string", Nullable: true},
					"code":    {Type: "string", Nullable: true},
				},
			},
			"CreateTaskRequest": {
				Type:     "object",
				Required: []string{"repository_id", "title"},
				Properties: map[string]*Schema{
					"repository_id": {Type: "string", Format: "uuid"},
					"title":         {Type: "string"},
					"description":   {Type: "string"},
					"priority":      {Type: "string", Enum: []interface{}{"low", "medium", "high", "urgent"}},
					"risk_level":    {Type: "string", Enum: []interface{}{"low", "medium", "high", "critical"}},
					"target_branch": {Type: "string"},
					"max_cost":      {Type: "number", Nullable: true},
					"spec":          {Type: "object", Nullable: true},
				},
			},
			"UpdateTaskRequest": {
				Type: "object",
				Properties: map[string]*Schema{
					"title":       {Type: "string"},
					"description": {Type: "string"},
					"priority":    {Type: "string", Enum: []interface{}{"low", "medium", "high", "urgent"}},
					"risk_level":  {Type: "string", Enum: []interface{}{"low", "medium", "high", "critical"}},
					"status":      {Type: "string", Enum: []interface{}{"backlog", "spec_review", "approved", "running", "reviewing", "pr_created", "deploying", "done", "failed", "cancelled"}},
				},
			},
			"CreateProjectRequest": {
				Type:     "object",
				Required: []string{"name", "slug"},
				Properties: map[string]*Schema{
					"name":        {Type: "string"},
					"slug":        {Type: "string"},
					"description": {Type: "string"},
				},
			},
			"RespondApprovalRequest": {
				Type:     "object",
				Required: []string{"response"},
				Properties: map[string]*Schema{
					"response":      {Type: "string", Enum: []interface{}{"approved", "rejected"}},
					"response_note": {Type: "string"},
				},
			},
			"CreateOrganizationRequest": {
				Type:     "object",
				Required: []string{"name", "slug"},
				Properties: map[string]*Schema{
					"name": {Type: "string"},
					"slug": {Type: "string"},
					"plan": {Type: "string", Enum: []interface{}{"free", "pro", "enterprise"}},
				},
			},
			"WebhookPayload": {
				Type: "object",
				Properties: map[string]*Schema{
					"event_type": {Type: "string"},
					"payload":    {Type: "object"},
				},
			},
			"PullRequest": {
				Type:     "object",
				Required: []string{"id", "task_id", "repository_id", "number", "title", "branch", "base_branch", "url", "state", "created_by", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":            {Type: "string", Format: "uuid"},
					"task_id":       {Type: "string", Format: "uuid"},
					"run_id":        {Type: "string", Format: "uuid", Nullable: true},
					"repository_id": {Type: "string", Format: "uuid"},
					"number":        {Type: "integer"},
					"title":         {Type: "string"},
					"body":          {Type: "string"},
					"branch":        {Type: "string"},
					"base_branch":   {Type: "string"},
					"url":           {Type: "string", Format: "uri"},
					"state":         {Type: "string", Enum: []interface{}{"open", "closed", "merged"}},
					"draft":         {Type: "boolean"},
					"created_by":    {Type: "string"},
					"merged_at":     {Type: "string", Format: "date-time", Nullable: true},
					"created_at":    {Type: "string", Format: "date-time"},
					"updated_at":    {Type: "string", Format: "date-time"},
				},
			},
			"MergePullRequestRequest": {
				Type: "object",
				Properties: map[string]*Schema{
					"merge_method": {Type: "string", Enum: []interface{}{"merge", "squash", "rebase"}, Nullable: true},
					"sha":          {Type: "string", Nullable: true},
				},
			},
			"DeployTaskRequest": {
				Type:     "object",
				Required: []string{"environment"},
				Properties: map[string]*Schema{
					"environment": {Type: "string"},
					"ref":         {Type: "string", Nullable: true},
				},
			},
			"DeploymentResponse": {
				Type:     "object",
				Required: []string{"id", "task_id", "environment", "ref", "provider", "status", "created_at"},
				Properties: map[string]*Schema{
					"id":          {Type: "string", Format: "uuid"},
					"task_id":     {Type: "string", Format: "uuid"},
					"environment": {Type: "string"},
					"ref":         {Type: "string"},
					"provider":    {Type: "string"},
					"status":      {Type: "string"},
					"url":         {Type: "string", Nullable: true},
					"created_at":  {Type: "string", Format: "date-time"},
				},
			},
			"Budget": {
				Type:     "object",
				Required: []string{"id", "organization_id", "name", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":                    {Type: "string", Format: "uuid"},
					"organization_id":       {Type: "string", Format: "uuid"},
					"project_id":            {Type: "string", Format: "uuid", Nullable: true},
					"name":                  {Type: "string"},
					"max_cost_per_run":      {Type: "number"},
					"max_runtime_minutes":   {Type: "integer"},
					"max_model_calls":       {Type: "integer"},
					"max_tool_calls":        {Type: "integer"},
					"max_shell_commands":    {Type: "integer"},
					"max_files_changed":     {Type: "integer"},
					"max_daily_spend":       {Type: "number"},
					"max_concurrent_agents": {Type: "integer"},
					"max_prs_per_day":       {Type: "integer"},
					"alerts_enabled":        {Type: "boolean"},
					"created_at":            {Type: "string", Format: "date-time"},
					"updated_at":            {Type: "string", Format: "date-time"},
				},
			},
			"AgentMessage": {
				Type:     "object",
				Required: []string{"id", "task_id", "from_agent", "to_agent", "message_type", "content", "created_at"},
				Properties: map[string]*Schema{
					"id":           {Type: "string", Format: "uuid"},
					"task_id":      {Type: "string", Format: "uuid"},
					"run_id":       {Type: "string", Format: "uuid", Nullable: true},
					"from_agent":   {Type: "string", Description: "Agent role, 'human', or 'system'"},
					"to_agent":     {Type: "string", Description: "Agent role or 'broadcast'"},
					"message_type": {Type: "string", Enum: []interface{}{"handoff", "review_comment", "blocker", "escalation", "watchdog", "decision", "question", "answer"}},
					"content":      {Type: "string"},
					"metadata":     {Type: "object", Nullable: true},
					"created_at":   {Type: "string", Format: "date-time"},
				},
			},
			"SecretReference": {
				Type:     "object",
				Required: []string{"id", "organization_id", "name", "scope", "provider", "key_path", "created_at", "updated_at"},
				Properties: map[string]*Schema{
					"id":              {Type: "string", Format: "uuid"},
					"organization_id": {Type: "string", Format: "uuid"},
					"project_id":      {Type: "string", Format: "uuid", Nullable: true},
					"name":            {Type: "string"},
					"scope":           {Type: "string", Enum: []interface{}{"dev", "staging", "prod"}},
					"provider":        {Type: "string", Enum: []interface{}{"sops", "env", "vault", "encrypted_db"}},
					"key_path":        {Type: "string", Description: "Path to the actual secret value"},
					"description":     {Type: "string"},
					"last_rotated_at": {Type: "string", Format: "date-time", Nullable: true},
					"created_at":      {Type: "string", Format: "date-time"},
					"updated_at":      {Type: "string", Format: "date-time"},
				},
			},
			"Artifact": {
				Type:     "object",
				Required: []string{"id", "artifact_type", "file_name", "file_path", "created_at"},
				Properties: map[string]*Schema{
					"id":            {Type: "string", Format: "uuid"},
					"agent_run_id":  {Type: "string", Format: "uuid", Nullable: true},
					"step_id":       {Type: "string", Format: "uuid", Nullable: true},
					"artifact_type": {Type: "string"},
					"file_name":     {Type: "string"},
					"file_path":     {Type: "string"},
					"mime_type":     {Type: "string", Nullable: true},
					"size_bytes":    {Type: "integer", Format: "int64"},
					"metadata":      {Type: "object", Nullable: true},
					"created_at":    {Type: "string", Format: "date-time"},
				},
			},
			"CreateBriefHandoffRequest": {
				Type:     "object",
				Required: []string{"repository_id"},
				Properties: map[string]*Schema{
					"repository_id":       {Type: "string", Format: "uuid"},
					"brief_project_id":    {Type: "string", Nullable: true},
					"brief_url":           {Type: "string", Format: "uri", Nullable: true},
					"brief_zip_url":       {Type: "string", Format: "uri", Nullable: true},
					"title":               {Type: "string", Nullable: true},
					"description":         {Type: "string", Nullable: true},
					"priority":            {Type: "string", Enum: []interface{}{"low", "medium", "high", "urgent"}, Nullable: true},
					"risk_level":          {Type: "string", Enum: []interface{}{"low", "medium", "high", "critical"}, Nullable: true},
					"target_branch":       {Type: "string", Nullable: true},
					"acceptance_criteria": {Type: "array", Items: &Schema{Type: "string"}},
					"documents": {
						Type: "array",
						Items: &Schema{
							Type: "object",
							Properties: map[string]*Schema{
								"slug":    {Type: "string"},
								"title":   {Type: "string"},
								"url":     {Type: "string", Format: "uri", Nullable: true},
								"content": {Type: "string", Nullable: true},
							},
						},
					},
					"constraints": {Type: "object", Nullable: true},
				},
			},
			"StartRunResponse": {
				Type:     "object",
				Required: []string{"run_id", "status"},
				Properties: map[string]*Schema{
					"run_id": {Type: "string", Format: "uuid"},
					"status": {Type: "string", Example: "queued"},
				},
			},
			"RetryRunResponse": {
				Type:     "object",
				Required: []string{"run_id", "original_run_id", "status"},
				Properties: map[string]*Schema{
					"run_id":          {Type: "string", Format: "uuid"},
					"original_run_id": {Type: "string", Format: "uuid"},
					"status":          {Type: "string", Example: "queued"},
				},
			},
			"RunEvent": {
				Type:     "object",
				Required: []string{"id", "agent_run_id", "event_type", "status", "created_at"},
				Properties: map[string]*Schema{
					"id":           {Type: "string", Format: "uuid"},
					"agent_run_id": {Type: "string", Format: "uuid"},
					"event_type":   {Type: "string"},
					"status":       {Type: "string"},
					"message":      {Type: "string", Nullable: true},
					"metadata":     {Type: "object", Nullable: true},
					"created_at":   {Type: "string", Format: "date-time"},
				},
			},
			"ReviewReport": {
				Type:     "object",
				Required: []string{"run_id", "summary", "findings", "risk_level", "approvable", "suggestions", "test_coverage", "security_notes", "diff_summary", "created_at"},
				Properties: map[string]*Schema{
					"run_id":         {Type: "string", Format: "uuid"},
					"summary":        {Type: "string"},
					"findings":       {Type: "array", Items: &Schema{Ref: "#/components/schemas/ReviewFinding"}},
					"risk_level":     {Type: "string", Enum: []interface{}{"low", "medium", "high", "critical"}},
					"approvable":     {Type: "boolean"},
					"suggestions":    {Type: "array", Items: &Schema{Type: "string"}},
					"test_coverage":  {Type: "string"},
					"security_notes": {Type: "string"},
					"diff_summary":   {Ref: "#/components/schemas/DiffSummary"},
					"created_at":     {Type: "string", Format: "date-time"},
				},
			},
			"ReviewFinding": {
				Type:     "object",
				Required: []string{"severity", "file", "line", "message", "category"},
				Properties: map[string]*Schema{
					"severity":   {Type: "string", Enum: []interface{}{"critical", "high", "medium", "low", "info"}},
					"file":       {Type: "string"},
					"line":       {Type: "integer"},
					"message":    {Type: "string"},
					"category":   {Type: "string", Enum: []interface{}{"correctness", "security", "performance", "style", "testing"}},
					"suggestion": {Type: "string", Nullable: true},
				},
			},
			"DiffSummary": {
				Type:     "object",
				Required: []string{"files_changed", "insertions", "deletions", "files"},
				Properties: map[string]*Schema{
					"files_changed": {Type: "integer"},
					"insertions":    {Type: "integer"},
					"deletions":     {Type: "integer"},
					"files":         {Type: "array", Items: &Schema{Ref: "#/components/schemas/FileChange"}},
				},
			},
			"FileChange": {
				Type:     "object",
				Required: []string{"path", "status", "insertions", "deletions"},
				Properties: map[string]*Schema{
					"path":         {Type: "string"},
					"status":       {Type: "string", Enum: []interface{}{"added", "modified", "deleted"}},
					"insertions":   {Type: "integer"},
					"deletions":    {Type: "integer"},
					"is_test":      {Type: "boolean"},
					"is_config":    {Type: "boolean"},
					"is_migration": {Type: "boolean"},
				},
			},
			"CreatePullRequestRequest": {
				Type: "object",
				Properties: map[string]*Schema{
					"approved": {Type: "boolean", Nullable: true},
				},
			},
			"RepoAnalysis": {
				Type:     "object",
				Required: []string{"repository_id", "languages", "package_managers", "frameworks", "test_commands", "build_commands", "entry_points", "has_dockerfile", "has_ci_config", "structure", "analyzed_at"},
				Properties: map[string]*Schema{
					"repository_id":    {Type: "string", Format: "uuid"},
					"languages":        {Type: "array", Items: &Schema{Ref: "#/components/schemas/RepoLanguage"}},
					"package_managers": {Type: "array", Items: &Schema{Type: "string"}},
					"frameworks":       {Type: "array", Items: &Schema{Type: "string"}},
					"test_commands":    {Type: "array", Items: &Schema{Type: "string"}},
					"build_commands":   {Type: "array", Items: &Schema{Type: "string"}},
					"dependencies":     {Type: "array", Items: &Schema{Ref: "#/components/schemas/RepoDependency"}, Nullable: true},
					"entry_points":     {Type: "array", Items: &Schema{Type: "string"}},
					"has_dockerfile":   {Type: "boolean"},
					"has_ci_config":    {Type: "boolean"},
					"structure":        {Type: "array", Items: &Schema{Ref: "#/components/schemas/DirEntry"}},
					"analyzed_at":      {Type: "string", Format: "date-time"},
				},
			},
			"RepoLanguage": {
				Type:     "object",
				Required: []string{"name", "file_count"},
				Properties: map[string]*Schema{
					"name":       {Type: "string"},
					"file_count": {Type: "integer"},
				},
			},
			"RepoDependency": {
				Type:     "object",
				Required: []string{"name", "version", "source", "type"},
				Properties: map[string]*Schema{
					"name":    {Type: "string"},
					"version": {Type: "string"},
					"source":  {Type: "string"},
					"type":    {Type: "string"},
				},
			},
			"DirEntry": {
				Type:     "object",
				Required: []string{"name", "is_dir"},
				Properties: map[string]*Schema{
					"name":   {Type: "string"},
					"is_dir": {Type: "boolean"},
				},
			},
			"RepoAnalysisPending": {
				Type:     "object",
				Required: []string{"repository_id", "status", "message", "clone_url"},
				Properties: map[string]*Schema{
					"repository_id": {Type: "string", Format: "uuid"},
					"status":        {Type: "string", Example: "pending_clone"},
					"message":       {Type: "string"},
					"clone_url":     {Type: "string", Format: "uri"},
				},
			},
			"WorkspaceFileEntry": {
				Type:     "object",
				Required: []string{"name", "path", "is_dir"},
				Properties: map[string]*Schema{
					"name":   {Type: "string"},
					"path":   {Type: "string"},
					"is_dir": {Type: "boolean"},
					"size":   {Type: "integer", Format: "int64"},
				},
			},
			"WorkspaceFileContent": {
				Type:     "object",
				Required: []string{"path", "content", "size"},
				Properties: map[string]*Schema{
					"path":    {Type: "string"},
					"content": {Type: "string"},
					"size":    {Type: "integer", Format: "int64"},
				},
			},
			"WriteFileRequest": {
				Type:     "object",
				Required: []string{"path", "content"},
				Properties: map[string]*Schema{
					"path":    {Type: "string"},
					"content": {Type: "string"},
				},
			},
			"PatchRequest": {
				Type:     "object",
				Required: []string{"patch"},
				Properties: map[string]*Schema{
					"patch": {Type: "string"},
				},
			},
			"ExecRequest": {
				Type:     "object",
				Required: []string{"command"},
				Properties: map[string]*Schema{
					"command": {Type: "string"},
					"timeout": {Type: "integer", Description: "Timeout in seconds, default 60"},
				},
			},
			"ExecResponse": {
				Type:     "object",
				Required: []string{"command", "stdout", "exit_code"},
				Properties: map[string]*Schema{
					"command":   {Type: "string"},
					"stdout":    {Type: "string"},
					"exit_code": {Type: "integer"},
				},
			},
			"StartServiceRequest": {
				Type:     "object",
				Required: []string{"command", "port"},
				Properties: map[string]*Schema{
					"command": {Type: "string"},
					"port":    {Type: "integer", Description: "Port must be between 0 and 65535"},
					"name":    {Type: "string", Nullable: true},
				},
			},
			"StartServiceResponse": {
				Type:     "object",
				Required: []string{"service_id", "status", "pid", "port"},
				Properties: map[string]*Schema{
					"service_id": {Type: "string"},
					"status":     {Type: "string", Example: "running"},
					"pid":        {Type: "integer"},
					"port":       {Type: "integer"},
					"log_path":   {Type: "string", Nullable: true},
				},
			},
			"StopServiceRequest": {
				Type:     "object",
				Required: []string{"service_id"},
				Properties: map[string]*Schema{
					"service_id": {Type: "string"},
				},
			},
			"CreateSecretRequest": {
				Type:     "object",
				Required: []string{"name", "value"},
				Properties: map[string]*Schema{
					"project_id":  {Type: "string", Format: "uuid", Nullable: true},
					"name":        {Type: "string"},
					"scope":       {Type: "string", Enum: []interface{}{"dev", "staging", "prod"}, Nullable: true},
					"description": {Type: "string", Nullable: true},
					"value":       {Type: "string"},
				},
			},
			"RotateSecretRequest": {
				Type:     "object",
				Required: []string{"value"},
				Properties: map[string]*Schema{
					"value": {Type: "string"},
				},
			},
			"AuditLog": {
				Type:     "object",
				Required: []string{"id", "organization_id", "actor_type", "action", "resource_type", "created_at"},
				Properties: map[string]*Schema{
					"id":              {Type: "string", Format: "uuid"},
					"organization_id": {Type: "string", Format: "uuid"},
					"actor_type":      {Type: "string"},
					"actor_id":        {Type: "string", Nullable: true},
					"action":          {Type: "string"},
					"resource_type":   {Type: "string"},
					"resource_id":     {Type: "string", Nullable: true},
					"details":         {Type: "object", Nullable: true},
					"ip_address":      {Type: "string", Nullable: true},
					"user_agent":      {Type: "string", Nullable: true},
					"created_at":      {Type: "string", Format: "date-time"},
				},
			},
		},
		SecuritySchemes: map[string]SecurityScheme{
			"bearerAuth": {
				Type:         "http",
				Scheme:       "bearer",
				BearerFormat: "JWT",
				Description:  "JWT token obtained from GitHub OAuth callback",
			},
		},
	}
}

// buildPaths defines all API endpoints with their parameters and responses.
func buildPaths() map[string]PathItem {
	paths := map[string]PathItem{}

	// Health
	paths["/health"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Health"},
			Summary:     "Health check",
			OperationID: "healthCheck",
			Responses: map[string]Response{
				"200": {
					Description: "Service is healthy",
					Content: map[string]MediaType{
						"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
							"status": {Type: "string"},
						}}},
					},
				},
			},
		},
	}
	paths["/ready"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Health"},
			Summary:     "Readiness check",
			OperationID: "readyCheck",
			Responses: map[string]Response{
				"200": {Description: "Service is ready"},
				"503": {Description: "Service is not ready"},
			},
		},
	}

	// Auth
	paths["/api/v1/auth/github"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Auth"},
			Summary:     "Initiate GitHub OAuth flow",
			OperationID: "githubAuthRedirect",
			Responses: map[string]Response{
				"302": {Description: "Redirects to GitHub OAuth"},
			},
		},
	}
	paths["/api/v1/auth/github/callback"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Auth"},
			Summary:     "GitHub OAuth callback",
			OperationID: "githubAuthCallback",
			Parameters: []Parameter{
				{Name: "code", In: "query", Required: true, Schema: &Schema{Type: "string"}},
				{Name: "state", In: "query", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Authentication successful, returns JWT token"},
				"400": {Description: "Invalid request"},
			},
		},
	}

	// Organizations
	paths["/api/v1/organizations"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Organizations"},
			Summary:     "List organizations",
			OperationID: "listOrganizations",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Responses: map[string]Response{
				"200": {Description: "List of organizations", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Organization"}}},
				}},
				"401": {Description: "Unauthorized"},
			},
		},
		Post: &Operation{
			Tags:        []string{"Organizations"},
			Summary:     "Create organization",
			OperationID: "createOrganization",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateOrganizationRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Organization created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Organization"}},
				}},
				"400": {Description: "Invalid request"},
				"401": {Description: "Unauthorized"},
			},
		},
	}
	paths["/api/v1/organizations/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Organizations"},
			Summary:     "Get organization",
			OperationID: "getOrganization",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Organization details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Organization"}},
				}},
				"404": {Description: "Organization not found"},
			},
		},
	}

	// Projects
	paths["/api/v1/organizations/{orgID}/projects"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Projects"},
			Summary:     "List projects",
			OperationID: "listProjects",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Description: "Organization ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of projects", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Project"}}},
				}},
			},
		},
		Post: &Operation{
			Tags:        []string{"Projects"},
			Summary:     "Create project",
			OperationID: "createProject",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Description: "Organization ID", Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateProjectRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Project created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Project"}},
				}},
				"400": {Description: "Invalid request"},
			},
		},
	}
	paths["/api/v1/projects/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Projects"},
			Summary:     "Get project",
			OperationID: "getProject",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Project details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Project"}},
				}},
				"404": {Description: "Project not found"},
			},
		},
	}

	// Repositories
	paths["/api/v1/projects/{projectID}/repositories"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Repositories"},
			Summary:     "List repositories",
			OperationID: "listRepositories",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of repositories", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Repository"}}},
				}},
			},
		},
		Post: &Operation{
			Tags:        []string{"Repositories"},
			Summary:     "Connect repository",
			OperationID: "connectRepository",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"owner": {Type: "string"},
						"name":  {Type: "string"},
					}}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Repository connected", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Repository"}},
				}},
			},
		},
	}
	paths["/api/v1/repositories/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Repositories"},
			Summary:     "Get repository",
			OperationID: "getRepository",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Repository details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Repository"}},
				}},
				"404": {Description: "Repository not found"},
			},
		},
		Delete: &Operation{
			Tags:        []string{"Repositories"},
			Summary:     "Disconnect repository",
			OperationID: "disconnectRepository",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"204": {Description: "Repository disconnected"},
				"404": {Description: "Repository not found"},
			},
		},
	}
	paths["/api/v1/repositories/{id}/sync"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Repositories"},
			Summary:     "Sync repository",
			OperationID: "syncRepository",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Sync started"},
				"404": {Description: "Repository not found"},
			},
		},
	}

	// Tasks
	paths["/api/v1/projects/{projectID}/tasks"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "List tasks",
			OperationID: "listTasks",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
				{Name: "status", In: "query", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of tasks", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Task"}}},
				}},
			},
		},
		Post: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Create task",
			OperationID: "createTask",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateTaskRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Task created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Task"}},
				}},
				"400": {Description: "Invalid request"},
			},
		},
	}
	paths["/api/v1/projects/{projectID}/voice-tasks"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks", "Integrations"},
			Summary:     "Create voice task",
			OperationID: "createVoiceTask",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateVoiceTaskRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Voice task created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Task"}},
				}},
			},
		},
	}
	paths["/api/v1/tasks/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Get task",
			OperationID: "getTask",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Task details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Task"}},
				}},
				"404": {Description: "Task not found"},
			},
		},
		Patch: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Update task",
			OperationID: "updateTask",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/UpdateTaskRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Task updated", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Task"}},
				}},
				"400": {Description: "Invalid request"},
			},
		},
	}
	paths["/api/v1/tasks/{id}/approve-spec"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Approve task spec",
			OperationID: "approveSpec",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Spec approved"},
				"400": {Description: "Task not in spec_review status"},
			},
		},
	}
	paths["/api/v1/tasks/{id}/spec"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Get task spec",
			Description: "Returns the generated technical specification for a task.",
			OperationID: "getTaskSpec",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Task ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Task spec", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/TaskSpec"}},
				}},
				"404": {Description: "Task or spec not found"},
			},
		},
	}
	paths["/api/v1/tasks/{id}/generate-spec"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Generate task spec",
			Description: "Generates and persists a task specification, then transitions the task to spec_review.",
			OperationID: "generateSpec",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Spec generated", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{
						Type: "object",
						Properties: map[string]*Schema{
							"status":  {Type: "string", Example: "spec_review"},
							"message": {Type: "string", Example: "Spec generated"},
							"spec_id": {Type: "string", Format: "uuid"},
						},
						Required: []string{"status", "message", "spec_id"},
					}},
				}},
				"400": {Description: "Task cannot generate a spec from its current status"},
				"404": {Description: "Task not found"},
			},
		},
	}
	paths["/api/v1/tasks/{id}/cancel"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Cancel task",
			OperationID: "cancelTask",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Task cancelled"},
				"400": {Description: "Task cannot be cancelled"},
			},
		},
	}

	// Agent Runs
	paths["/api/v1/tasks/{taskID}/runs"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "List agent runs",
			OperationID: "listAgentRuns",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "taskID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of agent runs", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/AgentRun"}}},
				}},
			},
		},
	}
	paths["/api/v1/runs/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "Get agent run",
			OperationID: "getAgentRun",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Agent run details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/AgentRun"}},
				}},
				"404": {Description: "Agent run not found"},
			},
		},
	}
	paths["/api/v1/runs/{id}/steps"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "List agent steps",
			OperationID: "listAgentSteps",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of agent steps", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/AgentStep"}}},
				}},
			},
		},
	}
	paths["/api/v1/runs/{id}/stream"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "Stream agent run updates (SSE)",
			OperationID: "streamAgentRun",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "SSE stream of run updates"},
			},
		},
	}

	// Approvals
	paths["/api/v1/tasks/{taskID}/approvals"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Approvals"},
			Summary:     "List approvals",
			OperationID: "listApprovals",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "taskID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of approvals", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Approval"}}},
				}},
			},
		},
	}
	paths["/api/v1/approvals/{id}/respond"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Approvals"},
			Summary:     "Respond to approval",
			OperationID: "respondApproval",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/RespondApprovalRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Approval responded"},
				"400": {Description: "Invalid request"},
				"404": {Description: "Approval not found"},
			},
		},
	}

	// Policies
	paths["/api/v1/organizations/{orgID}/policies"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Policies"},
			Summary:     "List policies",
			OperationID: "listPolicies",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of policies", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Policy"}}},
				}},
			},
		},
		Post: &Operation{
			Tags:        []string{"Policies"},
			Summary:     "Create policy",
			OperationID: "createPolicy",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Policy"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Policy created"},
			},
		},
	}

	// Audit Logs
	paths["/api/v1/organizations/{orgID}/audit-logs"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Audit Logs"},
			Summary:     "List audit logs",
			OperationID: "listAuditLogs",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
				{Name: "limit", In: "query", Description: "Maximum number of entries to return (1-1000)", Schema: &Schema{Type: "integer"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of audit log entries", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/AuditLog"}}},
				}},
			},
		},
	}

	// Dashboard
	paths["/api/v1/organizations/{orgID}/dashboard"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Dashboard"},
			Summary:     "Get dashboard data",
			OperationID: "getDashboard",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Dashboard data", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/DashboardData"}},
				}},
			},
		},
	}

	// Integrations
	paths["/api/v1/integrations/providers"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "List integration providers",
			OperationID: "listIntegrationProviders",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Responses: map[string]Response{
				"200": {Description: "List of integration providers", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/IntegrationProvider"}}},
				}},
			},
		},
	}
	paths["/api/v1/organizations/{orgID}/integrations"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "List integrations",
			OperationID: "listIntegrations",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of integrations", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Integration"}}},
				}},
			},
		},
		Post: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "Create integration",
			OperationID: "createIntegration",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateIntegrationRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Integration created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Integration"}},
				}},
			},
		},
	}
	paths["/api/v1/integrations/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "Get integration",
			OperationID: "getIntegration",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Integration", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Integration"}},
				}},
				"404": {Description: "Integration not found"},
			},
		},
		Patch: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "Update integration",
			OperationID: "updateIntegration",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/UpdateIntegrationRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Integration updated", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Integration"}},
				}},
			},
		},
		Delete: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "Delete integration",
			OperationID: "deleteIntegration",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"204": {Description: "Integration deleted"},
			},
		},
	}
	paths["/api/v1/integrations/{id}/verify"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Integrations"},
			Summary:     "Verify integration credentials",
			OperationID: "verifyIntegration",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Verification result", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/IntegrationVerifyResponse"}},
				}},
				"400": {Description: "Integration has no credentials to verify"},
				"404": {Description: "Integration not found"},
				"503": {Description: "Secret manager not configured"},
			},
		},
	}

	// Pull Request merge
	paths["/api/v1/pull-requests/{id}/merge"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Pull Requests"},
			Summary:     "Merge a pull request",
			Description: "Merges a pull request on GitHub after authorization and transitions the task to done.",
			OperationID: "mergePullRequest",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/MergePullRequestRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Pull request merged", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/PullRequest"}},
				}},
				"400": {Description: "Invalid request or task status"},
				"403": {Description: "Merge denied by policy"},
				"404": {Description: "Pull request not found"},
				"409": {Description: "Pull request already merged or merge conflict"},
				"423": {Description: "Merge requires approval"},
				"502": {Description: "GitHub API error"},
				"503": {Description: "GitHub token not configured"},
			},
		},
	}

	// Task deploy
	paths["/api/v1/tasks/{id}/deploy"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Deploy a task",
			Description: "Triggers a deployment for a task after capability authorization and records a deployment.",
			OperationID: "deployTask",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/DeployTaskRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Deployment created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/DeploymentResponse"}},
				}},
				"400": {Description: "Invalid request or task status"},
				"403": {Description: "Deploy denied by policy"},
				"404": {Description: "Task or repository not found"},
				"423": {Description: "Deploy requires approval"},
				"502": {Description: "Deployment provider error"},
				"503": {Description: "Deploy token not configured"},
			},
		},
	}

	// Task actions
	paths["/api/v1/projects/{projectID}/brief-handoffs"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks"},
			Summary:     "Create brief handoff task",
			Description: "Creates a task from a Dev Plan Builder's Brief handoff.",
			OperationID: "createBriefHandoff",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateBriefHandoffRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Brief handoff task created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Task"}},
				}},
				"400": {Description: "Invalid request"},
			},
		},
	}
	paths["/api/v1/tasks/{id}/start-run"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Tasks", "Agent Runs"},
			Summary:     "Start an agent run",
			Description: "Starts an implementer agent run for an approved task.",
			OperationID: "startRun",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Task ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"201": {Description: "Agent run started", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/StartRunResponse"}},
				}},
				"400": {Description: "Task not in approved status"},
				"404": {Description: "Task not found"},
			},
		},
	}

	// Agent run actions
	paths["/api/v1/runs/{id}/retry"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "Retry a failed agent run",
			Description: "Creates a new queued run from a failed or cancelled run.",
			OperationID: "retryRun",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Run ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"201": {Description: "Agent run retried", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/RetryRunResponse"}},
				}},
				"400": {Description: "Run cannot be retried"},
				"404": {Description: "Agent run not found"},
			},
		},
	}
	paths["/api/v1/runs/{id}/events"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "List run events",
			Description: "Returns high-level lifecycle events for an agent run.",
			OperationID: "getRunEvents",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Run ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of run events", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/RunEvent"}}},
				}},
				"404": {Description: "Agent run not found"},
			},
		},
	}
	paths["/api/v1/runs/{id}/cancel"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "Cancel agent run",
			OperationID: "cancelAgentRun",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Run ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Agent run cancelled", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"status": {Type: "string"},
						"id":     {Type: "string"},
					}}},
				}},
				"404": {Description: "Agent run not found or already finished"},
			},
		},
	}

	// Reviews
	paths["/api/v1/runs/{runId}/review"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "Get review report",
			OperationID: "getReview",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "runId", In: "path", Required: true, Description: "Run ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Review report", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/ReviewReport"}},
				}},
				"404": {Description: "Review report not found"},
			},
		},
		Post: &Operation{
			Tags:        []string{"Agent Runs"},
			Summary:     "Request review",
			Description: "Triggers a manual review for an agent run.",
			OperationID: "requestReview",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "runId", In: "path", Required: true, Description: "Run ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Review report", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/ReviewReport"}},
				}},
				"404": {Description: "Agent run not found"},
			},
		},
	}

	// Organization approvals
	paths["/api/v1/organizations/{orgID}/approvals"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Approvals"},
			Summary:     "List organization approvals",
			Description: "Returns all pending approvals across an organization's projects.",
			OperationID: "listOrganizationApprovals",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Description: "Organization ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of pending approvals", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Approval"}}},
				}},
			},
		},
	}

	// Pull Requests
	paths["/api/v1/projects/{projectID}/pull-requests"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Pull Requests"},
			Summary:     "List pull requests",
			OperationID: "listPullRequests",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "projectID", In: "path", Required: true, Description: "Project ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of pull requests", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/PullRequest"}}},
				}},
			},
		},
	}
	paths["/api/v1/pull-requests/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Pull Requests"},
			Summary:     "Get pull request",
			OperationID: "getPullRequest",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Pull request details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/PullRequest"}},
				}},
				"404": {Description: "Pull request not found"},
			},
		},
	}
	paths["/api/v1/tasks/{taskId}/pull-request"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Pull Requests", "Tasks"},
			Summary:     "Create pull request",
			Description: "Creates a pull request for a task after approval.",
			OperationID: "createPullRequest",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "taskId", In: "path", Required: true, Description: "Task ID", Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreatePullRequestRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Pull request created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/PullRequest"}},
				}},
				"400": {Description: "Invalid request or task status"},
				"409": {Description: "Pending approval exists"},
			},
		},
	}

	// Repository analysis
	paths["/api/v1/repositories/{id}/analyze"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Repositories"},
			Summary:     "Analyze repository",
			Description: "Triggers and returns an analysis of a repository's structure, package managers, frameworks, and test commands.",
			OperationID: "analyzeRepo",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Repository analysis", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/RepoAnalysis"}},
				}},
				"202": {Description: "Repository needs to be cloned first", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/RepoAnalysisPending"}},
				}},
				"404": {Description: "Repository not found"},
			},
		},
	}

	// Workspaces
	paths["/api/v1/tasks/{taskID}/workspaces"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "List task workspaces",
			OperationID: "listTaskWorkspaces",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "taskID", In: "path", Required: true, Description: "Task ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of workspaces", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/Workspace"}}},
				}},
			},
		},
	}
	paths["/api/v1/workspaces/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Get workspace",
			OperationID: "getWorkspace",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Workspace details", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Workspace"}},
				}},
				"404": {Description: "Workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/destroy"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Destroy workspace",
			OperationID: "destroyWorkspace",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Workspace destroyed", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"status": {Type: "string"},
						"id":     {Type: "string"},
					}}},
				}},
				"404": {Description: "Workspace not found or already destroyed"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/diff"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Get workspace diff",
			OperationID: "getWorkspaceDiff",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Git diff for the workspace", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"diff": {Type: "string"},
					}}},
				}},
				"404": {Description: "Workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/files"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "List workspace files",
			OperationID: "listWorkspaceFiles",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
				{Name: "path", In: "query", Description: "Relative directory path", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of files", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/WorkspaceFileEntry"}}},
				}},
				"403": {Description: "Path traversal detected"},
				"404": {Description: "Workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/files/content"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Read workspace file",
			OperationID: "readWorkspaceFile",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
				{Name: "path", In: "query", Required: true, Description: "Relative file path", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "File content", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/WorkspaceFileContent"}},
				}},
				"400": {Description: "Path query parameter is required"},
				"403": {Description: "Path traversal detected"},
				"404": {Description: "File or workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/files/write"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Write workspace file",
			OperationID: "writeWorkspaceFile",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/WriteFileRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "File written", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"status": {Type: "string"},
						"path":   {Type: "string"},
					}}},
				}},
				"400": {Description: "Invalid request"},
				"403": {Description: "Path traversal detected or operation denied"},
				"404": {Description: "Workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/patch"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Apply workspace patch",
			OperationID: "applyWorkspacePatch",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/PatchRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Patch applied", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"status": {Type: "string"},
						"output": {Type: "string"},
					}}},
				}},
				"400": {Description: "Invalid request"},
				"403": {Description: "Operation denied"},
				"404": {Description: "Workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/exec"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Execute workspace command",
			OperationID: "execWorkspaceCommand",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/ExecRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Command output", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/ExecResponse"}},
				}},
				"400": {Description: "Invalid command"},
				"403": {Description: "Operation denied"},
				"404": {Description: "Workspace not found"},
				"504": {Description: "Command timed out"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/start-service"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Start workspace service",
			OperationID: "startWorkspaceService",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/StartServiceRequest"}},
				},
			},
			Responses: map[string]Response{
				"202": {Description: "Service started", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/StartServiceResponse"}},
				}},
				"400": {Description: "Invalid request"},
				"403": {Description: "Operation denied"},
				"404": {Description: "Workspace not found"},
			},
		},
	}
	paths["/api/v1/workspaces/{id}/stop-service"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Workspaces"},
			Summary:     "Stop workspace service",
			OperationID: "stopWorkspaceService",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/StopServiceRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Service stopped", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "object", Properties: map[string]*Schema{
						"service_id": {Type: "string"},
						"status":     {Type: "string"},
					}}},
				}},
				"400": {Description: "Invalid request"},
				"403": {Description: "Operation denied"},
				"404": {Description: "Service or workspace not found"},
			},
		},
	}

	// Artifacts
	paths["/api/v1/artifacts/{id}"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Artifacts"},
			Summary:     "Get artifact",
			Description: "Returns artifact metadata or streams the artifact file.",
			OperationID: "getArtifact",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Artifact metadata or binary content"},
				"404": {Description: "Artifact not found"},
			},
		},
	}

	// Secrets
	paths["/api/v1/organizations/{orgID}/secrets"] = PathItem{
		Get: &Operation{
			Tags:        []string{"Secrets"},
			Summary:     "List secrets",
			OperationID: "listSecrets",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Description: "Organization ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "List of secrets", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Type: "array", Items: &Schema{Ref: "#/components/schemas/SecretReference"}}},
				}},
			},
		},
		Post: &Operation{
			Tags:        []string{"Secrets"},
			Summary:     "Create secret",
			Description: "Stores an encrypted secret reference for the organization.",
			OperationID: "createSecret",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "orgID", In: "path", Required: true, Description: "Organization ID", Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/CreateSecretRequest"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Secret created", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/SecretReference"}},
				}},
				"400": {Description: "Invalid request"},
				"403": {Description: "Operation denied"},
				"503": {Description: "Encrypted secret storage is not configured"},
			},
		},
	}
	paths["/api/v1/secrets/{id}/rotate"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Secrets"},
			Summary:     "Rotate secret",
			Description: "Updates the value of an existing encrypted secret.",
			OperationID: "rotateSecret",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Secret ID", Schema: &Schema{Type: "string"}},
			},
			RequestBody: &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/RotateSecretRequest"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Secret rotated", Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/SecretReference"}},
				}},
				"400": {Description: "Invalid request"},
				"403": {Description: "Operation denied"},
				"404": {Description: "Secret not found"},
				"503": {Description: "Encrypted secret storage is not configured"},
			},
		},
	}
	paths["/api/v1/secrets/{id}"] = PathItem{
		Delete: &Operation{
			Tags:        []string{"Secrets"},
			Summary:     "Delete secret",
			Description: "Soft-deletes an encrypted secret.",
			OperationID: "deleteSecret",
			Security:    []SecurityRequirement{{"bearerAuth": {}}},
			Parameters: []Parameter{
				{Name: "id", In: "path", Required: true, Description: "Secret ID", Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"204": {Description: "Secret deleted"},
				"400": {Description: "Invalid request"},
				"403": {Description: "Operation denied"},
				"404": {Description: "Secret not found"},
				"503": {Description: "Encrypted secret storage is not configured"},
			},
		},
	}

	// Webhooks
	paths["/api/v1/webhooks/github"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Webhooks"},
			Summary:     "GitHub webhook handler",
			OperationID: "githubWebhook",
			Responses: map[string]Response{
				"200": {Description: "Webhook ping processed"},
				"202": {Description: "Webhook accepted for processing"},
				"400": {Description: "Invalid webhook payload"},
				"401": {Description: "Missing or invalid webhook signature"},
				"503": {Description: "Webhook processing or signing secret is unavailable"},
			},
		},
	}
	paths["/api/v1/webhooks/linear"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Webhooks"},
			Summary:     "Linear webhook handler",
			OperationID: "linearWebhook",
			Responses: map[string]Response{
				"200": {Description: "Webhook processed"},
				"202": {Description: "Webhook accepted for processing"},
				"400": {Description: "Invalid webhook payload"},
				"401": {Description: "Missing or invalid webhook signature"},
				"503": {Description: "Webhook processing or signing secret is unavailable"},
			},
		},
	}
	paths["/api/v1/webhooks/slack"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Webhooks"},
			Summary:     "Slack webhook handler",
			OperationID: "slackWebhook",
			Responses: map[string]Response{
				"200": {Description: "Webhook processed"},
				"202": {Description: "Webhook accepted for processing"},
				"400": {Description: "Invalid webhook payload"},
				"401": {Description: "Missing or invalid webhook signature"},
				"503": {Description: "Webhook processing or signing secret is unavailable"},
			},
		},
	}
	paths["/api/v1/webhooks/discord"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Webhooks"},
			Summary:     "Discord webhook handler",
			OperationID: "discordWebhook",
			Responses: map[string]Response{
				"200": {Description: "Webhook processed"},
				"202": {Description: "Webhook accepted for processing"},
				"400": {Description: "Invalid webhook payload"},
				"401": {Description: "Missing or invalid webhook signature"},
				"503": {Description: "Webhook processing or signing secret is unavailable"},
			},
		},
	}
	paths["/api/v1/webhooks/{provider}/{integrationID}"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Webhooks", "Integrations"},
			Summary:     "Integration webhook handler",
			OperationID: "integrationWebhook",
			Parameters: []Parameter{
				{Name: "provider", In: "path", Required: true, Schema: &Schema{Type: "string"}},
				{Name: "integrationID", In: "path", Required: true, Schema: &Schema{Type: "string"}},
			},
			Responses: map[string]Response{
				"200": {Description: "Command processed"},
				"202": {Description: "Webhook accepted"},
				"400": {Description: "Invalid payload"},
				"404": {Description: "Integration not found"},
			},
		},
	}

	return paths
}

// specJSON is the cached marshaled spec.
var specJSON []byte

// JSON returns the OpenAPI spec as JSON bytes.
func JSON() ([]byte, error) {
	if specJSON != nil {
		return specJSON, nil
	}
	var err error
	specJSON, err = json.MarshalIndent(BuildSpec(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal openapi spec: %w", err)
	}
	return specJSON, nil
}
