// Provision a new, isolated Firebase review account and sample workspace.
// Credentials go only to a new private file, never stdout or repository files.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

func run() error {
	project := flag.String("project", "", "Firebase and Firestore project")
	email := flag.String("email", "", "new review account email (existing accounts are never changed)")
	output := flag.String("credentials-file", "", "new private output file outside the repository")
	tokenFile := flag.String("access-token-file", "", "optional file containing an operator OAuth access token; otherwise use ADC")
	apply := flag.Bool("apply", false, "create the account and sample data")
	scheduleAt := flag.String("schedule-at", "", "sample schedule time in RFC3339 (default: 30 days from now)")
	flag.Parse()
	if *project == "" || *email == "" || *output == "" || !*apply {
		return errors.New("require -project, -email, -credentials-file and -apply")
	}
	at := time.Now().UTC().Add(30 * 24 * time.Hour)
	if *scheduleAt != "" {
		var err error
		at, err = time.Parse(time.RFC3339, *scheduleAt)
		if err != nil || !at.After(time.Now()) {
			return errors.New("schedule-at must be a future RFC3339 time")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := []option.ClientOption{option.WithQuotaProject(*project)}
	if *tokenFile != "" {
		raw, err := os.ReadFile(*tokenFile)
		if err != nil {
			return err
		}
		opts = append(opts, option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: strings.TrimSpace(string(raw))})))
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: *project}, opts...)
	if err != nil {
		return err
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return err
	}
	if _, err = client.GetUserByEmail(ctx, *email); err == nil {
		return errors.New("account already exists; refusing to modify it")
	} else if !auth.IsUserNotFound(err) {
		return err
	}
	db, err := firestore.NewClient(ctx, *project, opts...)
	if err != nil {
		return err
	}
	defer db.Close()
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		return err
	}
	password := hex.EncodeToString(secret)
	f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	// Save before making the account, so a later provisioning failure cannot lose its password.
	if err = json.NewEncoder(f).Encode(map[string]string{"email": *email, "password": password, "url": "https://kakeratta.net/?login=1&review=1&lang=en", "linking": "In Alexa account linking, select Review access and use these credentials."}); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	user, err := client.CreateUser(ctx, (&auth.UserToCreate{}).Email(*email).Password(password).DisplayName("Alexa Reviewer"))
	if err != nil {
		return err
	}
	if err = client.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{"judge": true}); err != nil {
		return err
	}
	key := sha256.Sum256([]byte(user.UID))
	workspace, err := store.NewFirestoreWorkspace(ctx, db, hex.EncodeToString(key[:]), nil)
	if err != nil {
		return err
	}
	defer workspace.Close()
	settings := workspace.ConfiguredSettings()
	settings.TimeZone = "UTC"
	if err = workspace.SaveSettings(settings); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, entry := range []timeline.Entry{
		{ID: "review-example", Root: "review-example", Kind: timeline.KindPost, Speaker: timeline.User, Text: "Example note: I would like to take a walk.", Language: "en", At: now.Add(-time.Minute)},
		{ID: "review-example-reply", Root: "review-example", Kind: timeline.KindRemark, Speaker: "routine", Text: "Example reply for testing: A short walk sounds like a good break. Enjoy some fresh air.", Language: "en", At: now},
	} {
		if err = workspace.Append(entry); err != nil {
			return err
		}
	}
	notify := false
	if _, err = workspace.SaveSchedule(store.Schedule{Skill: "routine", At: at, Note: "Review example: go for a walk", Notify: &notify, UserRequested: true}); err != nil {
		return err
	}
	fmt.Println("Review account and sample data created. Credentials are in the private output file.")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
