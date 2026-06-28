package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ai-dev-control-plane/cli/internal/client"
	"github.com/ai-dev-control-plane/cli/internal/config"
)

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	baseURL := fs.String("base-url", "", "API base URL (required)")
	_ = fs.Parse(args)

	if *baseURL == "" {
		return fmt.Errorf("--base-url is required")
	}

	fmt.Print("API token: ")
	reader := bufio.NewReader(os.Stdin)
	tokenBytes, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))

	cfg := &config.Config{BaseURL: *baseURL, Token: token}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Println("Login saved.")
	return nil
}

func runTasksList(args []string) error {
	fs := flag.NewFlagSet("tasks list", flag.ExitOnError)
	projectID := fs.String("project-id", "", "Project ID (required)")
	_ = fs.Parse(args)

	if *projectID == "" {
		return fmt.Errorf("--project-id is required")
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	var tasks []Task
	if err := c.Get(context.Background(), "/api/v1/projects/"+url.PathEscape(*projectID)+"/tasks", &tasks); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPRIORITY\tTITLE")
	for _, t := range tasks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Priority, t.Title)
	}
	return w.Flush()
}

func runTasksGet(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tasks get <id>")
	}
	id := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	var task Task
	if err := c.Get(context.Background(), "/api/v1/tasks/"+url.PathEscape(id), &task); err != nil {
		return err
	}
	return printJSON(task)
}

func runTasksCreate(args []string) error {
	fs := flag.NewFlagSet("tasks create", flag.ExitOnError)
	projectID := fs.String("project-id", "", "Project ID (required)")
	title := fs.String("title", "", "Task title (required)")
	description := fs.String("description", "", "Task description")
	repositoryID := fs.String("repository-id", "", "Repository ID (required)")
	_ = fs.Parse(args)

	if *projectID == "" || *title == "" || *repositoryID == "" {
		return fmt.Errorf("--project-id, --title, and --repository-id are required")
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	payload := CreateTaskRequest{
		RepositoryID: *repositoryID,
		Title:        *title,
		Description:  *description,
	}
	var task Task
	path := "/api/v1/projects/" + url.PathEscape(*projectID) + "/tasks"
	if err := c.Post(context.Background(), path, payload, &task); err != nil {
		return err
	}
	return printJSON(task)
}

func runRunsList(args []string) error {
	fs := flag.NewFlagSet("runs list", flag.ExitOnError)
	taskID := fs.String("task-id", "", "Task ID (required)")
	_ = fs.Parse(args)

	if *taskID == "" {
		return fmt.Errorf("--task-id is required")
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	var runs []AgentRun
	path := "/api/v1/tasks/" + url.PathEscape(*taskID) + "/runs"
	if err := c.Get(context.Background(), path, &runs); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tROLE\tCREATED_AT")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.ID, r.Status, r.AgentRole, r.CreatedAt)
	}
	return w.Flush()
}

func runRunsLogs(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: runs logs <id>")
	}
	id := args[0]

	c, err := newClient()
	if err != nil {
		return err
	}

	path := "/api/v1/runs/" + url.PathEscape(id) + "/stream"
	headers := http.Header{}
	headers.Set("Accept", "text/event-stream")
	resp, err := c.Do(context.Background(), http.MethodGet, path, nil, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if data == "" {
			continue
		}
		var evt RunStreamEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			fmt.Printf("[sse] %s\n", data)
			continue
		}
		fmt.Printf("[%s] %s\n", evt.RunID, evt.Status)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

func runApprovalsList(args []string) error {
	fs := flag.NewFlagSet("approvals list", flag.ExitOnError)
	orgID := fs.String("org-id", "", "Organization ID (required)")
	_ = fs.Parse(args)

	if *orgID == "" {
		return fmt.Errorf("--org-id is required")
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	var approvals []Approval
	path := "/api/v1/organizations/" + url.PathEscape(*orgID) + "/approvals"
	if err := c.Get(context.Background(), path, &approvals); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tREQUESTED_BY\tRESPONSE\tCREATED_AT")
	for _, a := range approvals {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.ID, a.ApprovalType, a.RequestedBy, a.Response, a.CreatedAt)
	}
	return w.Flush()
}

func runApprovalsRespond(args []string) error {
	fs := flag.NewFlagSet("approvals respond", flag.ExitOnError)
	response := fs.String("response", "", "Response: approved or rejected (required)")
	_ = fs.Parse(args)

	if len(fs.Args()) < 1 {
		return fmt.Errorf("usage: approvals respond --response approved|rejected <id>")
	}
	id := fs.Args()[0]

	if *response != "approved" && *response != "rejected" {
		return fmt.Errorf("--response must be approved or rejected")
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	payload := RespondApprovalRequest{Response: *response}
	var approval Approval
	path := "/api/v1/approvals/" + url.PathEscape(id) + "/respond"
	if err := c.Post(context.Background(), path, payload, &approval); err != nil {
		return err
	}
	return printJSON(approval)
}

func newClient() (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("not logged in; run 'dev-plane login'")
	}
	return client.New(cfg.BaseURL, cfg.Token), nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
