// Package github reads and acts on a pull request through GitHub's API,
// as Lody's pull request panel does, with a token the workspace's GitHub
// App, or the user's own authorization, mints for this client.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// API is GitHub's API root.
var API = "https://api.github.com"

var prURL = regexp.MustCompile(`^https://github\.com/([\w.-]+/[\w.-]+)/pull/(\d+)`)

// ParsePR reads a pull request's repository and number from its URL.
func ParsePR(u string) (repo string, number int, ok bool) {
	m := prURL.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return "", 0, false
	}
	n, _ := strconv.Atoi(m[2])
	return m[1], n, n > 0
}

// Error is GitHub's answer to a request it refused.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("GitHub: %s (%d)", e.Message, e.Status)
	}
	return fmt.Sprintf("GitHub: HTTP %d", e.Status)
}

// IsStatus reports whether err is GitHub refusing with status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// Client calls GitHub with a token.
type Client struct {
	Token string
	HTTP  *http.Client
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, API+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		json.Unmarshal(data, &e)
		return &Error{Status: res.StatusCode, Message: e.Message}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// User is who wrote something.
type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

// PullRequest is a pull request's details.
type PullRequest struct {
	NodeID         string    `json:"node_id"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	State          string    `json:"state"` // open or closed
	Draft          bool      `json:"draft"`
	Merged         bool      `json:"merged"`
	Mergeable      *bool     `json:"mergeable"`
	MergeableState string    `json:"mergeable_state"` // clean, dirty, blocked, behind, unstable, unknown, draft
	HTMLURL        string    `json:"html_url"`
	User           User      `json:"user"`
	CreatedAt      time.Time `json:"created_at"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changed_files"`
	Commits        int       `json:"commits"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// Status is the pull request's state as Lody names it: open, draft,
// merged or closed. A draft is one GitHub marks as such, or whose
// mergeability it reports as a draft's, as Lody's isDraftPr.
func (pr *PullRequest) Status() string {
	switch {
	case pr.Merged:
		return "merged"
	case pr.State == "closed":
		return "closed"
	case pr.Draft || pr.MergeableState == "draft":
		return "draft"
	}
	return "open"
}

// ReadyForReviewPending is whether GitHub has yet to settle the pull
// request out of draft, as Lody's
// isPullRequestReadyForReviewTransitionPending: still a draft, or open
// while its mergeability is still being worked out.
func (pr *PullRequest) ReadyForReviewPending() bool {
	switch pr.Status() {
	case "merged", "closed":
		return false
	case "draft":
		return true
	}
	return pr.Mergeable == nil || pr.MergeableState == "unknown"
}

func (c *Client) PullRequest(ctx context.Context, repo string, n int) (*PullRequest, error) {
	var pr PullRequest
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(repo), n), nil, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// CheckRun is a CI check on a commit.
type CheckRun struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`     // queued, in_progress, completed
	Conclusion  string     `json:"conclusion"` // success, failure, neutral, cancelled, skipped, timed_out, action_required, stale
	HTMLURL     string     `json:"html_url"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	App         struct {
		Name string `json:"name"`
	} `json:"app"`
}

// Failed is whether the check failed.
func (r CheckRun) Failed() bool {
	switch r.Conclusion {
	case "failure", "timed_out", "cancelled", "action_required":
		return true
	}
	return false
}

// Running is whether the check is still going.
func (r CheckRun) Running() bool { return r.Status != "completed" }

// CheckRuns are the checks on a commit as Lody's githubFetchCheckRuns
// reads them: the latest attempt of each, in the order of their names.
func (c *Client) CheckRuns(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	var out struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/commits/%s/check-runs?per_page=100", repoPath(repo), url.PathEscape(sha)), nil, &out)
	return LatestCheckRuns(out.CheckRuns), err
}

// LatestCheckRuns keeps the latest attempt of each check, by its app and
// name, as Lody's selectLatestCheckRuns: a re-run supersedes the run it
// repeats. They come in the order of their names.
func LatestCheckRuns(runs []CheckRun) []CheckRun {
	if len(runs) == 0 {
		return runs
	}
	at := map[string]int{}
	var out []CheckRun
	for _, r := range runs {
		key := r.App.Name + "\x00" + r.Name
		if i, ok := at[key]; ok {
			if r.ID > out[i].ID {
				out[i] = r
			}
			continue
		}
		at[key] = len(out)
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Review is a review of the pull request.
type Review struct {
	ID          int64     `json:"id"`
	User        User      `json:"user"`
	State       string    `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
	Body        string    `json:"body"`
	SubmittedAt time.Time `json:"submitted_at"`
}

func (c *Client) Reviews(ctx context.Context, repo string, n int) ([]Review, error) {
	var out []Review
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", repoPath(repo), n), nil, &out)
	return out, err
}

// Comment is a comment on the pull request's conversation.
type Comment struct {
	ID        int64     `json:"id"`
	User      User      `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Client) Comments(ctx context.Context, repo string, n int) ([]Comment, error) {
	var out []Comment
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d/comments?per_page=100", repoPath(repo), n), nil, &out)
	return out, err
}

// File is a file the pull request changes.
type File struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"` // added, removed, modified, renamed, copied, changed
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
	BlobURL          string `json:"blob_url"`
}

func (c *Client) Files(ctx context.Context, repo string, n int) ([]File, error) {
	var all []File
	for page := 1; page <= 30; page++ {
		var out []File
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/files?per_page=100&page=%d", repoPath(repo), n, page), nil, &out); err != nil {
			return all, err
		}
		all = append(all, out...)
		if len(out) < 100 {
			break
		}
	}
	return all, nil
}

// Merge merges the pull request: merge, squash or rebase. With sha, the
// head Lody read just before, GitHub refuses if the head has moved since,
// as Lody's githubMergePullRequest asks.
func (c *Client) Merge(ctx context.Context, repo string, n int, method, sha string) error {
	body := map[string]any{"merge_method": method}
	if sha != "" {
		body["sha"] = sha
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("%s/pulls/%d/merge", repoPath(repo), n), body, nil)
}

// Comment posts to the pull request's conversation.
func (c *Client) Comment(ctx context.Context, repo string, n int, body string) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(repo), n), map[string]any{"body": body}, nil)
}

// SetState closes or reopens the pull request.
func (c *Client) SetState(ctx context.Context, repo string, n int, state string) error {
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/pulls/%d", repoPath(repo), n), map[string]any{"state": state}, nil)
}

// DeleteBranch deletes a branch.
func (c *Client) DeleteBranch(ctx context.Context, repo, ref string) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/git/refs/heads/%s", repoPath(repo), url.PathEscape(ref)), nil, nil)
}

// ReadyForReview takes the pull request out of draft, which only GitHub's
// GraphQL API does.
func (c *Client) ReadyForReview(ctx context.Context, nodeID string) error {
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	err := c.do(ctx, http.MethodPost, "/graphql", map[string]any{
		"query":     "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }",
		"variables": map[string]any{"id": nodeID},
	}, &out)
	if err == nil && len(out.Errors) > 0 {
		err = errors.New(out.Errors[0].Message)
	}
	return err
}

// IssueOrPR is an open issue or pull request of a repository.
type IssueOrPR struct {
	Number    int
	Title     string
	URL       string
	PR        bool
	UpdatedAt time.Time
}

// IssuesAndPRs lists a repository's open issues and pull requests, the
// most recently updated first, at most 200, as Lody's # mentions read
// them: two pages of GitHub's issues, which hold both.
func (c *Client) IssuesAndPRs(ctx context.Context, repo string) ([]IssueOrPR, error) {
	type item struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		HTMLURL     string    `json:"html_url"`
		State       string    `json:"state"`
		UpdatedAt   time.Time `json:"updated_at"`
		PullRequest *struct{} `json:"pull_request"`
	}
	var pages [2][]item
	errs := make(chan error, 2)
	for i := range pages {
		go func(i int) {
			errs <- c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues?state=open&per_page=100&sort=updated&direction=desc&page=%d", repo, i+1), nil, &pages[i])
		}(i)
	}
	for range pages {
		if err := <-errs; err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	var out []IssueOrPR
	for _, page := range pages {
		for _, it := range page {
			key := fmt.Sprint(it.PullRequest != nil, it.Number)
			if it.State != "open" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, IssueOrPR{Number: it.Number, Title: it.Title, URL: it.HTMLURL, PR: it.PullRequest != nil, UpdatedAt: it.UpdatedAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

// DefaultBranch is the repository's default branch.
func (c *Client) DefaultBranch(ctx context.Context, repo string) (string, error) {
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, http.MethodGet, repoPath(repo), nil, &out); err != nil {
		return "", err
	}
	return out.DefaultBranch, nil
}

// ErrTooLarge is a file longer than a read allows.
var ErrTooLarge = errors.New("GitHub: file too large")

// FileAt reads a file of the repository at ref, as text of at most max
// bytes; a file GitHub does not have is a 404 Error.
func (c *Client) FileAt(ctx context.Context, repo, path, ref string, max int) (string, error) {
	var out struct {
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := c.do(ctx, http.MethodGet, repoPath(repo)+"/contents/"+path+"?ref="+url.QueryEscape(ref), nil, &out); err != nil {
		return "", err
	}
	if out.Size > max {
		return "", ErrTooLarge
	}
	if out.Encoding != "base64" {
		return out.Content, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	return string(b), err
}
