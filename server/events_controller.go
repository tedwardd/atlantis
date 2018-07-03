// Copyright 2017 HootSuite Media Inc.
//
// Licensed under the Apache License, Version 2.0 (the License);
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an AS IS BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Modified hereafter by contributors to runatlantis/atlantis.
//
package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"

	"github.com/google/go-github/github"
	"github.com/lkysow/go-gitlab"
	"github.com/runatlantis/atlantis/server/events"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/events/vcs"
	log "gopkg.in/inconshreveable/log15.v2"
)

const githubHeader = "X-Github-Event"
const gitlabHeader = "X-Gitlab-Event"
const reqIDSize = 7

// EventsController handles all webhook requests which signify 'events' in the
// VCS host, ex. GitHub.
type EventsController struct {
	CommandRunner events.CommandRunner
	PullCleaner   events.PullCleaner
	Logger        log.Logger
	Parser        events.EventParsing
	CommentParser events.CommentParsing
	// GithubWebHookSecret is the secret added to this webhook via the GitHub
	// UI that identifies this call as coming from GitHub. If empty, no
	// request validation is done.
	GithubWebHookSecret          []byte
	GithubRequestValidator       GithubRequestValidator
	GitlabRequestParserValidator GitlabRequestParserValidator
	// GitlabWebHookSecret is the secret added to this webhook via the GitLab
	// UI that identifies this call as coming from GitLab. If empty, no
	// request validation is done.
	GitlabWebHookSecret  []byte
	RepoWhitelistChecker *events.RepoWhitelistChecker
	// SupportedVCSHosts is which VCS hosts Atlantis was configured upon
	// startup to support.
	SupportedVCSHosts []models.VCSHostType
	VCSClient         vcs.ClientProxy
	TestingMode       bool
}

// Post handles POST webhook requests.
func (e *EventsController) Post(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(githubHeader) != "" {
		// Use part of the Github Delivery ID as request_id.
		ctxLogger := e.Logger.New("reqid", e.githubRequestID(r.Header.Get("X-Github-Delivery")))
		ctxLogger.Debug("handling GitHub post")

		if !e.supportsHost(models.Github) {
			e.respond(ctxLogger, w, log.LvlDebug, http.StatusBadRequest, "Ignoring request since not configured to support GitHub")
			return
		}
		e.handleGithubPost(ctxLogger, w, r)
		return
	} else if r.Header.Get(gitlabHeader) != "" {
		// GitLab doesn't include a request id so generate one.
		ctxLogger := e.Logger.New("reqid", e.genRequestID())
		ctxLogger.Debug("handling GitLab post")

		if !e.supportsHost(models.Gitlab) {
			e.respond(ctxLogger, w, log.LvlDebug, http.StatusBadRequest, "Ignoring request since not configured to support GitLab")
			return
		}
		e.handleGitlabPost(ctxLogger, w, r)
		return
	}
	e.respond(e.Logger, w, log.LvlDebug, http.StatusBadRequest, "Ignoring request")
}

func (e *EventsController) handleGithubPost(logger log.Logger, w http.ResponseWriter, r *http.Request) {
	// Validate the request against the optional webhook secret.
	payload, err := e.GithubRequestValidator.Validate(r, e.GithubWebHookSecret)
	if err != nil {
		e.respond(logger, w, log.LvlWarn, http.StatusBadRequest, err.Error())
		return
	}
	logger.Debug("request passed validation")

	event, _ := github.ParseWebHook(github.WebHookType(r), payload)
	switch event := event.(type) {
	case *github.IssueCommentEvent:
		logger.Debug("handling as comment event")
		e.HandleGithubCommentEvent(logger, w, event)
	case *github.PullRequestEvent:
		logger.Debug("handling as pull request event")
		e.HandleGithubPullRequestEvent(logger, w, event)
	default:
		e.respond(logger, w, log.LvlDebug, http.StatusOK, "Ignoring unsupported event")
	}
}

// HandleGithubCommentEvent handles comment events from GitHub where Atlantis
// commands can come from. It's exported to make testing easier.
func (e *EventsController) HandleGithubCommentEvent(logger log.Logger, w http.ResponseWriter, event *github.IssueCommentEvent) {
	if event.GetAction() != "created" {
		e.respond(logger, w, log.LvlDebug, http.StatusOK, "Ignoring comment event since action was not created")
		return
	}

	baseRepo, user, pullNum, err := e.Parser.ParseGithubIssueCommentEvent(event)
	if err != nil {
		e.respond(logger, w, log.LvlError, http.StatusBadRequest, "Failed parsing event", "err", err)
		return
	}

	// We pass in nil for maybeHeadRepo because the head repo data isn't
	// available in the GithubIssueComment event.
	e.handleCommentEvent(logger, w, baseRepo, nil, user, pullNum, event.Comment.GetBody(), models.Github)
}

// HandleGithubPullRequestEvent will delete any locks associated with the pull
// request if the event is a pull request closed event. It's exported to make
// testing easier.
func (e *EventsController) HandleGithubPullRequestEvent(logger log.Logger, w http.ResponseWriter, pullEvent *github.PullRequestEvent) {
	pull, baseRepo, headRepo, user, err := e.Parser.ParseGithubPullEvent(pullEvent)
	if err != nil {
		e.respond(logger, w, log.LvlError, http.StatusBadRequest, "Error parsing pull data", "err", err)
		return
	}
	var eventType string
	switch pullEvent.GetAction() {
	case "opened":
		eventType = OpenPullEvent
	case "synchronize":
		eventType = UpdatedPullEvent
	case "closed":
		eventType = ClosedPullEvent
	default:
		eventType = OtherPullEvent
	}
	logger.Info("identified event", "type", eventType)
	e.handlePullRequestEvent(logger, w, baseRepo, headRepo, pull, user, eventType)
}

const OpenPullEvent = "opened"
const UpdatedPullEvent = "updated"
const ClosedPullEvent = "closed"
const OtherPullEvent = "other"

func (e *EventsController) handlePullRequestEvent(logger log.Logger, w http.ResponseWriter, baseRepo models.Repo, headRepo models.Repo, pull models.PullRequest, user models.User, eventType string) {
	if !e.RepoWhitelistChecker.IsWhitelisted(baseRepo.FullName, baseRepo.VCSHost.Hostname) {
		// If the repo isn't whitelisted and we receive an opened pull request
		// event we comment back on the pull request that the repo isn't
		// whitelisted. This is because the user might be expecting Atlantis to
		// autoplan. For other events, we just ignore them.
		if eventType == OpenPullEvent {
			e.commentNotWhitelisted(logger, baseRepo, pull.Num)
		}
		e.respond(logger, w, log.LvlDebug, http.StatusForbidden, "Ignoring pull request event from non-whitelisted repo", "repo", baseRepo.FullName)
		return
	}

	switch eventType {
	case OpenPullEvent, UpdatedPullEvent:
		// If the pull request was opened or updated, we will try to autoplan.

		// Respond with success and then actually execute the command asynchronously.
		// We use a goroutine so that this function returns and the connection is
		// closed.
		fmt.Fprintln(w, "Processing...")

		logger.Info("executing autoplan")
		if !e.TestingMode {
			go e.CommandRunner.RunAutoplanCommand(logger, baseRepo, headRepo, pull, user)
		} else {
			// When testing we want to wait for everything to complete.
			e.CommandRunner.RunAutoplanCommand(logger, baseRepo, headRepo, pull, user)
		}
		return
	case ClosedPullEvent:
		// If the pull request was closed, we delete locks.
		if err := e.PullCleaner.CleanUpPull(baseRepo, pull); err != nil {
			e.respond(logger, w, log.LvlError, http.StatusInternalServerError, "Error cleaning pull request", "err", err)
			return
		}
		logger.Info("deleted locks and workspace", "repo", baseRepo.FullName, "pull", pull.Num)
		fmt.Fprintln(w, "Pull request cleaned successfully")
		return
	case OtherPullEvent:
		// Else we ignore the event.
		e.respond(logger, w, log.LvlDebug, http.StatusOK, "Ignoring non-actionable pull request event")
		return
	}
}

func (e *EventsController) handleGitlabPost(logger log.Logger, w http.ResponseWriter, r *http.Request) {
	event, err := e.GitlabRequestParserValidator.ParseAndValidate(r, e.GitlabWebHookSecret)
	if err != nil {
		e.respond(logger, w, log.LvlWarn, http.StatusBadRequest, err.Error())
		return
	}
	logger.Debug("request valid")

	switch event := event.(type) {
	case gitlab.MergeCommentEvent:
		logger.Debug("handling as comment event")
		e.HandleGitlabCommentEvent(logger, w, event)
	case gitlab.MergeEvent:
		logger.Debug("handling as pull request event")
		e.HandleGitlabMergeRequestEvent(logger, w, event)
	default:
		e.respond(logger, w, log.LvlDebug, http.StatusOK, "Ignoring unsupported event")
	}

}

// HandleGitlabCommentEvent handles comment events from GitLab where Atlantis
// commands can come from. It's exported to make testing easier.
func (e *EventsController) HandleGitlabCommentEvent(logger log.Logger, w http.ResponseWriter, event gitlab.MergeCommentEvent) {
	baseRepo, headRepo, user, err := e.Parser.ParseGitlabMergeCommentEvent(event)
	if err != nil {
		e.respond(logger, w, log.LvlError, http.StatusBadRequest, "Error parsing webhook", "err", err)
		return
	}
	e.handleCommentEvent(logger, w, baseRepo, &headRepo, user, event.MergeRequest.IID, event.ObjectAttributes.Note, models.Gitlab)
}

func (e *EventsController) handleCommentEvent(logger log.Logger, w http.ResponseWriter, baseRepo models.Repo, maybeHeadRepo *models.Repo, user models.User, pullNum int, comment string, vcsHost models.VCSHostType) {
	parseResult := e.CommentParser.Parse(comment, vcsHost)
	if parseResult.Ignore {
		truncated := comment
		truncateLen := 40
		if len(truncated) > truncateLen {
			truncated = comment[:truncateLen] + "..."
		}
		e.respond(logger, w, log.LvlDebug, http.StatusOK, "Ignoring non-command", "comment", truncated)
		return
	}
	logger.Info("parsed comment", "parse_result", parseResult.Command)

	// At this point we know it's a command we're not supposed to ignore, so now
	// we check if this repo is allowed to run commands in the first place.
	if !e.RepoWhitelistChecker.IsWhitelisted(baseRepo.FullName, baseRepo.VCSHost.Hostname) {
		e.commentNotWhitelisted(logger, baseRepo, pullNum)
		e.respond(logger, w, log.LvlWarn, http.StatusForbidden, "Repo not whitelisted", "repo", baseRepo.FullName)
		return
	}

	// If the command isn't valid or doesn't require processing, ex.
	// "atlantis help" then we just comment back immediately.
	// We do this here rather than earlier because we need access to the pull
	// variable to comment back on the pull request.
	if parseResult.CommentResponse != "" {
		if err := e.VCSClient.CreateComment(baseRepo, pullNum, parseResult.CommentResponse); err != nil {
			logger.Error("unable to comment on pull request", "err", err)
		}
		e.respond(logger, w, log.LvlInfo, http.StatusOK, "Commenting back on pull request")
		return
	}

	logger.Debug("executing command")
	fmt.Fprintln(w, "Processing...")
	if !e.TestingMode {
		// Respond with success and then actually execute the command asynchronously.
		// We use a goroutine so that this function returns and the connection is
		// closed.
		go e.CommandRunner.RunCommentCommand(logger, baseRepo, maybeHeadRepo, user, pullNum, parseResult.Command)
	} else {
		// When testing we want to wait for everything to complete.
		e.CommandRunner.RunCommentCommand(logger, baseRepo, maybeHeadRepo, user, pullNum, parseResult.Command)
	}
}

// HandleGitlabMergeRequestEvent will delete any locks associated with the pull
// request if the event is a merge request closed event. It's exported to make
// testing easier.
func (e *EventsController) HandleGitlabMergeRequestEvent(logger log.Logger, w http.ResponseWriter, event gitlab.MergeEvent) {
	pull, baseRepo, headRepo, user, err := e.Parser.ParseGitlabMergeEvent(event)
	if err != nil {
		e.respond(logger, w, log.LvlError, http.StatusBadRequest, "Error parsing webhook", "err", err)
		return
	}
	var eventType string
	switch event.ObjectAttributes.Action {
	case "open":
		eventType = OpenPullEvent
	case "update":
		eventType = UpdatedPullEvent
	case "merge", "close":
		eventType = ClosedPullEvent
	default:
		eventType = OtherPullEvent
	}
	logger.Info("identified event", "type", eventType)
	e.handlePullRequestEvent(logger, w, baseRepo, headRepo, pull, user, eventType)
}

// supportsHost returns true if h is in e.SupportedVCSHosts and false otherwise.
func (e *EventsController) supportsHost(h models.VCSHostType) bool {
	for _, supported := range e.SupportedVCSHosts {
		if h == supported {
			return true
		}
	}
	return false
}

func (e *EventsController) respond(logger log.Logger, w http.ResponseWriter, lvl log.Lvl, code int, msg string, logCtx ...interface{}) {
	switch lvl {
	case log.LvlDebug:
		logger.Debug(msg, logCtx...)
	case log.LvlInfo:
		logger.Info(msg, logCtx...)
	case log.LvlWarn:
		logger.Warn(msg, logCtx...)
	case log.LvlError:
		logger.Error(msg, logCtx...)
	}
	w.WriteHeader(code)
	fmt.Fprintln(w, msg)
}

// commentNotWhitelisted comments on the pull request that the repo is not
// whitelisted.
func (e *EventsController) commentNotWhitelisted(logger log.Logger, baseRepo models.Repo, pullNum int) {
	errMsg := "```\nError: This repo is not whitelisted for Atlantis.\n```"
	if err := e.VCSClient.CreateComment(baseRepo, pullNum, errMsg); err != nil {
		logger.Error("unable to comment on pull request", "err", err)
	}
}

func (e *EventsController) genRequestID() string {
	r := make([]byte, reqIDSize)
	_, err := rand.Read(r)
	if err != nil {
		return "genfail"
	}
	return hex.EncodeToString(r)
}

func (e *EventsController) githubRequestID(githubIDHeader string) string {
	remaining := int(math.Max(float64(reqIDSize-len(githubIDHeader)), 0))
	r := make([]byte, remaining)
	_, err := rand.Read(r)
	if err != nil {
		return "genfail"
	}
	return (githubIDHeader + hex.EncodeToString(r))[:reqIDSize]
}
