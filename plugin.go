package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"

	log "github.com/Sirupsen/logrus"
	"github.com/aymerick/douceur/inliner"
	"github.com/drone/drone-go/template"
	"github.com/jaytaylor/html2text"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"gopkg.in/gomail.v2"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

func OAuthGmailService(ctx context.Context, credentials, token string) (*gmail.Service, error) {
	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON([]byte(credentials), gmail.GmailReadonlyScope)
	if err != nil {
		return nil, err
	}

	tok := &oauth2.Token{}
	if err = json.Unmarshal([]byte(token), tok); err != nil {
		return nil, err
	}

	return gmail.NewService(ctx, option.WithTokenSource(
		config.TokenSource(ctx, tok)))
}

func SendEmailOAUTH2(srv *gmail.Service, msg *gomail.Message) (err error) {
	msgData := new(bytes.Buffer)
	if _, err = msg.WriteTo(msgData); err != nil {
		return
	}
	var message gmail.Message

	message.Raw = base64.URLEncoding.EncodeToString(msgData.Bytes())

	// Send the message
	_, err = srv.Users.Messages.Send("me", &message).Do()
	return
}

type (
	Repo struct {
		FullName string
		Owner    string
		Name     string
		SCM      string
		Link     string
		Avatar   string
		Branch   string
		Private  bool
		Trusted  bool
	}

	Remote struct {
		URL string
	}

	Author struct {
		Name   string
		Email  string
		Avatar string
	}

	Commit struct {
		Sha     string
		Ref     string
		Branch  string
		Link    string
		Message string
		Author  Author
	}

	Build struct {
		Number   int
		Event    string
		Status   string
		Link     string
		Created  int64
		Started  int64
		Finished int64
	}

	PrevBuild struct {
		Status string
		Number int
	}

	PrevCommit struct {
		Sha string
	}

	Prev struct {
		Build  PrevBuild
		Commit PrevCommit
	}

	Job struct {
		Status   string
		ExitCode int
		Started  int64
		Finished int64
	}

	Yaml struct {
		Signed   bool
		Verified bool
	}

	Config struct {
		From           string
		Recipients     []string
		RecipientsFile string
		RecipientsOnly bool
		Subject        string
		Body           string
		Attachment     string
		Attachments    []string
		Credentials    string
		Token          string
	}

	Plugin struct {
		Repo        Repo
		Remote      Remote
		Commit      Commit
		Build       Build
		Prev        Prev
		Job         Job
		Yaml        Yaml
		Tag         string
		PullRequest int
		DeployTo    string
		Config      Config
	}
)

// Exec will send emails over SMTP
func (p Plugin) Exec() error {
	gmService, err := OAuthGmailService(context.Background(), p.Config.Credentials, p.Config.Token)
	if !p.Config.RecipientsOnly {
		exists := false
		for _, recipient := range p.Config.Recipients {
			if recipient == p.Commit.Author.Email {
				exists = true
			}
		}

		if !exists {
			p.Config.Recipients = append(p.Config.Recipients, p.Commit.Author.Email)
		}
	}

	if p.Config.RecipientsFile != "" {
		f, err := os.Open(p.Config.RecipientsFile)
		if err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				p.Config.Recipients = append(p.Config.Recipients, scanner.Text())
			}
		} else {
			log.Errorf("Could not open RecipientsFile %s: %v", p.Config.RecipientsFile, err)
		}
	}

	type Context struct {
		Repo        Repo
		Remote      Remote
		Commit      Commit
		Build       Build
		Prev        Prev
		Job         Job
		Yaml        Yaml
		Tag         string
		PullRequest int
		DeployTo    string
	}
	ctx := Context{
		Repo:        p.Repo,
		Remote:      p.Remote,
		Commit:      p.Commit,
		Build:       p.Build,
		Prev:        p.Prev,
		Job:         p.Job,
		Yaml:        p.Yaml,
		Tag:         p.Tag,
		PullRequest: p.PullRequest,
		DeployTo:    p.DeployTo,
	}

	// Render body in HTML and plain text
	renderedBody, err := template.RenderTrim(p.Config.Body, ctx)
	if err != nil {
		log.Errorf("Could not render body template: %v", err)
		return err
	}
	html, err := inliner.Inline(renderedBody)
	if err != nil {
		log.Errorf("Could not inline rendered body: %v", err)
		return err
	}
	plainBody, err := html2text.FromString(html)
	if err != nil {
		log.Errorf("Could not convert html to text: %v", err)
		return err
	}

	// Render subject
	subject, err := template.RenderTrim(p.Config.Subject, ctx)
	if err != nil {
		log.Errorf("Could not render subject template: %v", err)
		return err
	}

	// Send emails
	message := gomail.NewMessage()
	for _, recipient := range p.Config.Recipients {
		if len(recipient) == 0 {
			continue
		}
		message.SetHeader("From", p.Config.From)
		message.SetAddressHeader("To", recipient, "")
		message.SetHeader("Subject", subject)
		message.AddAlternative("text/plain", plainBody)
		message.AddAlternative("text/html", html)

		if p.Config.Attachment != "" {
			attach(message, p.Config.Attachment)
		}

		for _, attachment := range p.Config.Attachments {
			attach(message, attachment)
		}

		if err := SendEmailOAUTH2(gmService, message); err != nil {
			log.Errorf("Could not send email to %q: %v", recipient, err)
			return err
		}
		message.Reset()
	}

	return nil
}

func attach(message *gomail.Message, attachment string) {
	if _, err := os.Stat(attachment); err == nil {
		message.Attach(attachment)
	}
}
