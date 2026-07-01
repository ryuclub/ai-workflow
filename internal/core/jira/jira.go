// Package jira 是对既有 jira_api.py 的薄封装：把它当外部工具 subprocess 调用，
// 解析其 JSON stdout。JIRA 逻辑（含 ADF 解析、凭据）仍以 Python 脚本为唯一真相源。
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// IssueSummary 对应 `jira_api.py search` 的列表项。
type IssueSummary struct {
	Key       string   `json:"key"`
	Summary   string   `json:"summary"`
	Status    string   `json:"status"`
	IssueType string   `json:"issuetype"`
	Assignee  *string  `json:"assignee"`
	DueDate   *string  `json:"duedate"`
	Labels    []string `json:"labels"`
	Parent    *string  `json:"parent"`
}

// Subtask 对应 `jira_api.py get` 的子任务项。
type Subtask struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

// Issue 对应 `jira_api.py get` 的单票（description 为 ADF JSON，原样透传）。
type Issue struct {
	Key         string          `json:"key"`
	Summary     string          `json:"summary"`
	Status      string          `json:"status"`
	IssueType   string          `json:"issuetype"`
	Assignee    *string         `json:"assignee"`
	Description json.RawMessage `json:"description"`
	Labels      []string        `json:"labels"`
	Parent      *string         `json:"parent"`
	Subtasks    []Subtask       `json:"subtasks"`
}

// Client 通过 python3 调用 jira_api.py。
type Client struct {
	Python string // 默认 python3
	Script string // jira_api.py 路径
}

// New 构造客户端。
func New(script string) *Client {
	return &Client{Python: "python3", Script: script}
}

func (c *Client) run(ctx context.Context, out any, args ...string) error {
	full := append([]string{c.Script}, args...)
	cmd := exec.CommandContext(ctx, c.Python, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("jira_api.py %v 失败: %v: %s", args, err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("解析 jira_api.py 输出失败: %v: %s", err, stdout.String())
	}
	return nil
}

// Search 按 JQL 列票。
func (c *Client) Search(ctx context.Context, jql string, max int) ([]IssueSummary, error) {
	var out []IssueSummary
	if err := c.run(ctx, &out, "search", jql, strconv.Itoa(max)); err != nil {
		return nil, err
	}
	return out, nil
}

// Get 读单票。
func (c *Client) Get(ctx context.Context, key string) (*Issue, error) {
	var out Issue
	if err := c.run(ctx, &out, "get", key); err != nil {
		return nil, err
	}
	return &out, nil
}

// Transition 按流转名变更工单状态。
func (c *Client) Transition(ctx context.Context, key, name string) error {
	var out map[string]any
	return c.run(ctx, &out, "transition", key, name)
}
