// Package dispatch transports persistent reaction requests through Pub/Sub.
package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/takeshy/kakeratta/internal/alarm"
	"github.com/takeshy/kakeratta/internal/store"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

type Publisher struct {
	service *pubsub.Service
	topic   string
}

func New(ctx context.Context, topic string, opts ...option.ClientOption) (*Publisher, error) {
	if !strings.HasPrefix(topic, "projects/") || !strings.Contains(topic, "/topics/") {
		return nil, errors.New("a full Pub/Sub topic name is required")
	}
	service, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &Publisher{service: service, topic: topic}, nil
}

func (p *Publisher) Publish(ctx context.Context, job store.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = p.service.Projects.Topics.Publish(p.topic, &pubsub.PublishRequest{Messages: []*pubsub.PubsubMessage{{Data: base64.StdEncoding.EncodeToString(data)}}}).Context(ctx).Do()
	return err
}

func Authenticate(r *http.Request, validate alarm.Validator, audience, account string) error {
	if audience == "" || account == "" {
		return errors.New("internal authentication is not configured")
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return errors.New("no identity token")
	}
	email, err := validate(r.Context(), token, audience)
	if err != nil {
		return err
	}
	if email != account {
		return errors.New("unexpected service account")
	}
	return nil
}

func ReadPush(r *http.Request, validate alarm.Validator, audience, account, subscription string) (store.Job, error) {
	var job store.Job
	if err := Authenticate(r, validate, audience, account); err != nil {
		return job, err
	}
	var envelope struct {
		Subscription string `json:"subscription"`
		Message      struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<10)).Decode(&envelope); err != nil {
		return job, err
	}
	if subscription == "" || envelope.Subscription != subscription {
		return job, errors.New("unexpected subscription")
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Message.Data)
	if err != nil {
		return job, err
	}
	if err := json.Unmarshal(data, &job); err != nil {
		return job, err
	}
	return job, nil
}
