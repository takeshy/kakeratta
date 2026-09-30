// Package alarm wakes a workspace at a time through Cloud Tasks.
//
// A workspace asks to be woken at the next time anything of its own comes
// due (see persona.Runner.SetAlarm). Here that becomes a task scheduled for
// then, which calls back into the service, which queues the workspace's due
// reaction. Nothing runs between: with nothing due there is no task, and the
// service may scale to zero.
package alarm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/cloudtasks/v2"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
)

// maxAhead is the furthest Cloud Tasks schedules a task; a wake asked for
// later is set for then, and sets the rest itself.
const maxAhead = 29 * 24 * time.Hour

// bucket is how finely task names divide time. Setting the alarm twice for
// the same bucket sets one task, which is all a wake needs; Cloud Tasks keeps
// a name for about an hour after its task ran, so a finer name would not be
// set again in that hour either.
const bucket = 30 * time.Second

// Tasks sets workspaces' alarms as Cloud Tasks tasks.
type Tasks struct {
	service *cloudtasks.Service
	queue   string
	url     string
	account string
	now     func() time.Time
}

// NewTasks sends wakes through queue (projects/P/locations/L/queues/Q) to url,
// with an identity token of the service account. Options are for tests.
func NewTasks(ctx context.Context, queue, url, account string, opts ...option.ClientOption) (*Tasks, error) {
	if queue == "" || url == "" || account == "" {
		return nil, errors.New("a Cloud Tasks queue, a URL and a service account are all required")
	}
	service, err := cloudtasks.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &Tasks{service: service, queue: queue, url: url, account: account, now: time.Now}, nil
}

// Set wakes the workspace at at, or at once if that has passed. Asking twice
// for the same time asks once.
func (t *Tasks) Set(ctx context.Context, key string, at time.Time) error {
	now := t.now()
	if at.Before(now) {
		at = now
	}
	if limit := now.Add(maxAhead); at.After(limit) {
		at = limit
	}
	body, err := json.Marshal(map[string]string{"workspace": key})
	if err != nil {
		return err
	}
	task := &cloudtasks.Task{
		Name: fmt.Sprintf("%s/tasks/due-%s-%d", t.queue, key, at.Unix()/int64(bucket/time.Second)),
		HttpRequest: &cloudtasks.HttpRequest{
			HttpMethod: "POST",
			Url:        t.url,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       base64.StdEncoding.EncodeToString(body),
			OidcToken:  &cloudtasks.OidcToken{ServiceAccountEmail: t.account, Audience: t.url},
		},
	}
	if at.After(now.Add(2 * time.Second)) {
		task.ScheduleTime = at.UTC().Format(time.RFC3339)
	}
	_, err = t.service.Projects.Locations.Queues.Tasks.Create(t.queue, &cloudtasks.CreateTaskRequest{Task: task}).Context(ctx).Do()
	var apiError *googleapi.Error
	if errors.As(err, &apiError) && apiError.Code == http.StatusConflict {
		return nil // Set already.
	}
	return err
}

// Validator checks the identity token a task carries and returns the email of
// the service account it was issued for.
type Validator func(ctx context.Context, token, audience string) (email string, err error)

// GoogleValidator checks a Google-signed identity token.
func GoogleValidator(ctx context.Context, token, audience string) (string, error) {
	payload, err := idtoken.Validate(ctx, token, audience)
	if err != nil {
		return "", err
	}
	email, _ := payload.Claims["email"].(string)
	if verified, _ := payload.Claims["email_verified"].(bool); !verified {
		return "", errors.New("the token's email is not verified")
	}
	return email, nil
}

// Workspace reads a wake a task delivered: it is refused unless it carries an
// identity token issued for the audience to the service account the tasks run
// as, and it names the workspace to wake.
func Workspace(r *http.Request, validate Validator, audience, account string) (string, error) {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return "", errors.New("no identity token")
	}
	email, err := validate(r.Context(), token, audience)
	if err != nil {
		return "", err
	}
	if email != account {
		return "", errors.New("the token was issued to another account")
	}
	var body struct {
		Workspace string `json:"workspace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<10)).Decode(&body); err != nil || body.Workspace == "" {
		return "", errors.New("no workspace named")
	}
	return body.Workspace, nil
}
