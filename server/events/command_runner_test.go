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
package events_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/runatlantis/atlantis/server/logging"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/google/go-github/github"
	. "github.com/petergtz/pegomock"
	"github.com/runatlantis/atlantis/server/events"
	"github.com/runatlantis/atlantis/server/events/mocks"
	"github.com/runatlantis/atlantis/server/events/mocks/matchers"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/events/models/fixtures"
	"github.com/runatlantis/atlantis/server/events/vcs"
	vcsmocks "github.com/runatlantis/atlantis/server/events/vcs/mocks"
	. "github.com/runatlantis/atlantis/testing"
)

var projectCommandBuilder *mocks.MockProjectCommandBuilder
var eventParsing *mocks.MockEventParsing
var vcsClient *vcsmocks.MockClientProxy
var ghStatus *mocks.MockCommitStatusUpdater
var githubGetter *mocks.MockGithubPullGetter
var gitlabGetter *mocks.MockGitlabMergeRequestGetter
var ch events.DefaultCommandRunner
var historyHandler logging.HistoryHandler
var historyLogger log.Logger

func setup(t *testing.T) {
	RegisterMockTestingT(t)
	projectCommandBuilder = mocks.NewMockProjectCommandBuilder()
	eventParsing = mocks.NewMockEventParsing()
	ghStatus = mocks.NewMockCommitStatusUpdater()
	vcsClient = vcsmocks.NewMockClientProxy()
	githubGetter = mocks.NewMockGithubPullGetter()
	gitlabGetter = mocks.NewMockGitlabMergeRequestGetter()
	historyLogger = log.New()
	historyHandler = logging.HistoryHandler{DefaultHandler: log.DiscardHandler()}
	historyLogger.SetHandler(&historyHandler)
	projectCommandRunner := mocks.NewMockProjectCommandRunner()
	ch = events.DefaultCommandRunner{
		VCSClient:                vcsClient,
		CommitStatusUpdater:      ghStatus,
		EventParser:              eventParsing,
		MarkdownRenderer:         &events.MarkdownRenderer{},
		GithubPullGetter:         githubGetter,
		GitlabMergeRequestGetter: gitlabGetter,
		AllowForkPRs:             false,
		AllowForkPRsFlag:         "allow-fork-prs-flag",
		ProjectCommandBuilder:    projectCommandBuilder,
		ProjectCommandRunner:     projectCommandRunner,
	}
}

func TestRunCommentCommand_LogPanics(t *testing.T) {
	t.Log("if there is a panic it is commented back on the pull request")
	setup(t)
	ch.AllowForkPRs = true // Lets us get to the panic code.
	defer func() { ch.AllowForkPRs = false }()
	When(ghStatus.Update(fixtures.GithubRepo, fixtures.Pull, vcs.Pending, events.Plan)).ThenPanic("panic")
	ch.RunCommentCommand(historyLogger, fixtures.GithubRepo, &fixtures.GithubRepo, fixtures.User, 1, nil)
	_, _, comment := vcsClient.VerifyWasCalledOnce().CreateComment(matchers.AnyModelsRepo(), AnyInt(), AnyString()).GetCapturedArguments()
	Assert(t, strings.Contains(comment, "Error: goroutine panic"), "comment should be about a goroutine panic")
}

func TestRunCommentCommand_NoGithubPullGetter(t *testing.T) {
	t.Log("if DefaultCommandRunner was constructed with a nil GithubPullGetter an error should be logged")
	setup(t)
	ch.GithubPullGetter = nil
	logger := historyLogger
	ch.RunCommentCommand(logger, fixtures.GithubRepo, &fixtures.GithubRepo, fixtures.User, 1, nil)
	exp := "Atlantis not configured to support GitHub"
	act := historyHandler.History.String()
	Assert(t, strings.Contains(act, exp), "exp %q in %q", exp, act)
}

func TestRunCommentCommand_NoGitlabMergeGetter(t *testing.T) {
	t.Log("if DefaultCommandRunner was constructed with a nil GitlabMergeRequestGetter an error should be logged")
	setup(t)
	ch.GitlabMergeRequestGetter = nil
	ch.RunCommentCommand(historyLogger, fixtures.GitlabRepo, &fixtures.GitlabRepo, fixtures.User, 1, nil)
	exp := "Atlantis not configured to support GitLab"
	act := historyHandler.History.String()
	Assert(t, strings.Contains(act, exp), "exp %q in %q", exp, act)
}

func TestRunCommentCommand_GithubPullErr(t *testing.T) {
	t.Log("if getting the github pull request fails an error should be logged")
	setup(t)
	When(githubGetter.GetPullRequest(fixtures.GithubRepo, fixtures.Pull.Num)).ThenReturn(nil, errors.New("err"))
	ch.RunCommentCommand(historyLogger, fixtures.GithubRepo, &fixtures.GithubRepo, fixtures.User, fixtures.Pull.Num, nil)
	exp := "making pull request API call to GitHub: err"
	act := historyHandler.History.String()
	Assert(t, strings.Contains(act, exp), "exp %q in %q", exp, act)
}

func TestRunCommentCommand_GitlabMergeRequestErr(t *testing.T) {
	t.Log("if getting the gitlab merge request fails an error should be logged")
	setup(t)
	When(gitlabGetter.GetMergeRequest(fixtures.GithubRepo.FullName, fixtures.Pull.Num)).ThenReturn(nil, errors.New("err"))
	ch.RunCommentCommand(historyLogger, fixtures.GitlabRepo, &fixtures.GitlabRepo, fixtures.User, fixtures.Pull.Num, nil)
	exp := "making merge request API call to GitLab: err"
	act := historyHandler.History.String()
	Assert(t, strings.Contains(act, exp), "exp %q in %q", exp, act)
}

func TestRunCommentCommand_GithubPullParseErr(t *testing.T) {
	t.Log("if parsing the returned github pull request fails an error should be logged")
	setup(t)
	var pull github.PullRequest
	When(githubGetter.GetPullRequest(fixtures.GithubRepo, fixtures.Pull.Num)).ThenReturn(&pull, nil)
	When(eventParsing.ParseGithubPull(&pull)).ThenReturn(fixtures.Pull, fixtures.GithubRepo, fixtures.GitlabRepo, errors.New("err"))

	ch.RunCommentCommand(historyLogger, fixtures.GithubRepo, &fixtures.GithubRepo, fixtures.User, fixtures.Pull.Num, nil)
	exp := "extracting required fields from comment data: err"
	act := historyHandler.History.String()
	Assert(t, strings.Contains(act, exp), "exp %q in %q", exp, act)
}

func TestRunCommentCommand_ForkPRDisabled(t *testing.T) {
	t.Log("if a command is run on a forked pull request and this is disabled atlantis should" +
		" comment saying that this is not allowed")
	setup(t)
	ch.AllowForkPRs = false // by default it's false so don't need to reset
	var pull github.PullRequest
	modelPull := models.PullRequest{State: models.Open}
	When(githubGetter.GetPullRequest(fixtures.GithubRepo, fixtures.Pull.Num)).ThenReturn(&pull, nil)

	headRepo := fixtures.GithubRepo
	headRepo.FullName = "forkrepo/atlantis"
	headRepo.Owner = "forkrepo"
	When(eventParsing.ParseGithubPull(&pull)).ThenReturn(modelPull, modelPull.BaseRepo, headRepo, nil)

	ch.RunCommentCommand(historyLogger, fixtures.GithubRepo, nil, fixtures.User, fixtures.Pull.Num, nil)
	vcsClient.VerifyWasCalledOnce().CreateComment(fixtures.GithubRepo, modelPull.Num, "Atlantis commands can't be run on fork pull requests. To enable, set --"+ch.AllowForkPRsFlag)
}

func TestRunCommentCommand_ClosedPull(t *testing.T) {
	t.Log("if a command is run on a closed pull request atlantis should" +
		" comment saying that this is not allowed")
	setup(t)
	pull := &github.PullRequest{
		State: github.String("closed"),
	}
	modelPull := models.PullRequest{State: models.Closed}
	When(githubGetter.GetPullRequest(fixtures.GithubRepo, fixtures.Pull.Num)).ThenReturn(pull, nil)
	When(eventParsing.ParseGithubPull(pull)).ThenReturn(modelPull, modelPull.BaseRepo, fixtures.GithubRepo, nil)

	ch.RunCommentCommand(historyLogger, fixtures.GithubRepo, &fixtures.GithubRepo, fixtures.User, fixtures.Pull.Num, nil)
	vcsClient.VerifyWasCalledOnce().CreateComment(fixtures.GithubRepo, modelPull.Num, "Atlantis commands can't be run on closed pull requests")
}

func TestRunCommentCommand_FullRun(t *testing.T) {
	pull := &github.PullRequest{
		State: github.String("closed"),
	}
	expCmdResult := events.CommandResult{
		ProjectResults: []events.ProjectResult{
			{
				RepoRelDir: ".",
				Workspace:  "default",
			},
		},
	}
	for _, c := range []events.CommandName{events.Plan, events.Apply} {
		setup(t)
		cmd := events.NewCommentCommand(".", nil, c, false, "default", "")
		When(githubGetter.GetPullRequest(fixtures.GithubRepo, fixtures.Pull.Num)).ThenReturn(pull, nil)
		When(eventParsing.ParseGithubPull(pull)).ThenReturn(fixtures.Pull, fixtures.GithubRepo, fixtures.GithubRepo, nil)

		cmdCtx := models.ProjectCommandContext{RepoRelDir: "."}
		switch c {
		case events.Plan:
			When(projectCommandBuilder.BuildPlanCommand(matchers.AnyPtrToEventsCommandContext(), matchers.AnyPtrToEventsCommentCommand())).ThenReturn(cmdCtx, nil)
		case events.Apply:
			When(projectCommandBuilder.BuildApplyCommand(matchers.AnyPtrToEventsCommandContext(), matchers.AnyPtrToEventsCommentCommand())).ThenReturn(cmdCtx, nil)
		}

		ch.RunCommentCommand(historyLogger, fixtures.GithubRepo, nil, fixtures.User, fixtures.Pull.Num, cmd)

		ghStatus.VerifyWasCalledOnce().Update(fixtures.GithubRepo, fixtures.Pull, vcs.Pending, c)
		_, _, response := ghStatus.VerifyWasCalledOnce().UpdateProjectResult(matchers.AnyPtrToEventsCommandContext(), matchers.AnyEventsCommandName(), matchers.AnyEventsCommandResult()).GetCapturedArguments()
		Equals(t, expCmdResult, response)
		vcsClient.VerifyWasCalledOnce().CreateComment(matchers.AnyModelsRepo(), AnyInt(), AnyString())
	}
}

func TestRunAutoplanCommands(t *testing.T) {
	expCmdResult := events.CommandResult{
		ProjectResults: []events.ProjectResult{
			{
				RepoRelDir: ".",
				Workspace:  "default",
			},
		},
	}
	setup(t)
	When(projectCommandBuilder.BuildAutoplanCommands(matchers.AnyPtrToEventsCommandContext())).ThenReturn([]models.ProjectCommandContext{{RepoRelDir: ".", Workspace: "default"}}, nil)
	ch.RunAutoplanCommand(log.New(), fixtures.GithubRepo, fixtures.GithubRepo, fixtures.Pull, fixtures.User)

	ghStatus.VerifyWasCalledOnce().Update(fixtures.GithubRepo, fixtures.Pull, vcs.Pending, events.Plan)
	_, _, response := ghStatus.VerifyWasCalledOnce().UpdateProjectResult(matchers.AnyPtrToEventsCommandContext(), matchers.AnyEventsCommandName(), matchers.AnyEventsCommandResult()).GetCapturedArguments()
	Equals(t, expCmdResult, response)
	vcsClient.VerifyWasCalledOnce().CreateComment(matchers.AnyModelsRepo(), AnyInt(), AnyString())
}
