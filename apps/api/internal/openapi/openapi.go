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
					"status":                {Type: "string", Enum: []interface{}{"backlog", "spec_review", "approved", "running", "reviewing", "pr_created", "done", "failed", "cancelled"}},
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
					"status":      {Type: "string", Enum: []interface{}{"backlog", "spec_review", "approved", "running", "reviewing", "pr_created", "done", "failed", "cancelled"}},
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
			},
			Responses: map[string]Response{
				"200": {Description: "List of audit log entries"},
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
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Integration"}},
				},
			},
			Responses: map[string]Response{
				"201": {Description: "Integration created"},
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
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/Integration"}},
				},
			},
			Responses: map[string]Response{
				"200": {Description: "Integration updated"},
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

	// Webhooks
	paths["/api/v1/webhooks/github"] = PathItem{
		Post: &Operation{
			Tags:        []string{"Webhooks"},
			Summary:     "GitHub webhook handler",
			OperationID: "githubWebhook",
			Responses: map[string]Response{
				"200": {Description: "Webhook processed"},
				"400": {Description: "Invalid webhook payload"},
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
