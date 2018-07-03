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
package events

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/pkg/errors"
	"github.com/runatlantis/atlantis/server/events/models"
)

const workingDirPrefix = "repos"

//go:generate pegomock generate -m --use-experimental-model-gen --package mocks -o mocks/mock_working_dir.go WorkingDir

// WorkingDir handles the workspace on disk for running commands.
type WorkingDir interface {
	// Clone git clones headRepo, checks out the branch and then returns the
	// absolute path to the root of the cloned repo.
	Clone(log log.Logger, baseRepo models.Repo, headRepo models.Repo, p models.PullRequest, workspace string) (string, error)
	// GetWorkingDir returns the path to the workspace for this repo and pull.
	// If workspace does not exist on disk, error will be of type os.IsNotExist.
	GetWorkingDir(r models.Repo, p models.PullRequest, workspace string) (string, error)
	// Delete deletes the workspace for this repo and pull.
	Delete(r models.Repo, p models.PullRequest) error
	DeleteForWorkspace(r models.Repo, p models.PullRequest, workspace string) error
}

// FileWorkspace implements WorkingDir with the file system.
type FileWorkspace struct {
	DataDir string
	// TestingOverrideCloneURL can be used during testing to override the URL
	// that is cloned. If it's empty then we clone normally.
	TestingOverrideCloneURL string
}

// Clone git clones headRepo, checks out the branch and then returns the absolute
// path to the root of the cloned repo. If the repo already exists and is at
// the right commit it does nothing. This is to support running commands in
// multiple dirs of the same repo without deleting existing plans.
func (w *FileWorkspace) Clone(
	logger log.Logger,
	baseRepo models.Repo,
	headRepo models.Repo,
	p models.PullRequest,
	workspace string) (string, error) {
	cloneDir := w.cloneDir(baseRepo, p, workspace)

	// If the directory already exists, check if it's at the right commit.
	// If so, then we do nothing.
	if _, err := os.Stat(cloneDir); err == nil {
		logger.Debug(fmt.Sprintf("clone directory %q already exists, checking if it's at the right commit", cloneDir))
		revParseCmd := exec.Command("git", "rev-parse", "HEAD") // #nosec
		revParseCmd.Dir = cloneDir
		output, err := revParseCmd.CombinedOutput()
		if err != nil {
			logger.Error("will re-clone repo, could not determine if was at correct commit", "command", "git rev-parse HEAD", "err", err, "output", string(output))
			return w.forceClone(logger, cloneDir, headRepo, p)
		}
		currCommit := strings.Trim(string(output), "\n")
		if currCommit == p.HeadCommit {
			logger.Debug(fmt.Sprintf("repo is at correct commit %q so will not re-clone", p.HeadCommit))
			return cloneDir, nil
		}
		logger.Debug(fmt.Sprintf("repo was already cloned but is not at correct commit, wanted %q got %q", p.HeadCommit, currCommit))
		// We'll fall through to re-clone.
	}

	// Otherwise we clone the repo.
	return w.forceClone(logger, cloneDir, headRepo, p)
}

func (w *FileWorkspace) forceClone(log log.Logger,
	cloneDir string,
	headRepo models.Repo,
	p models.PullRequest) (string, error) {

	err := os.RemoveAll(cloneDir)
	if err != nil {
		return "", errors.Wrapf(err, "deleting dir %q before cloning", cloneDir)
	}

	// Create the directory and parents if necessary.
	log.Info("creating dir %q", cloneDir)
	if err := os.MkdirAll(cloneDir, 0700); err != nil {
		return "", errors.Wrap(err, "creating new workspace")
	}

	log.Info("git cloning %q into %q", headRepo.SanitizedCloneURL, cloneDir)
	cloneURL := headRepo.CloneURL
	if w.TestingOverrideCloneURL != "" {
		cloneURL = w.TestingOverrideCloneURL
	}
	cloneCmd := exec.Command("git", "clone", cloneURL, cloneDir) // #nosec
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		return "", errors.Wrapf(err, "cloning %s: %s", headRepo.SanitizedCloneURL, string(output))
	}

	// Check out the branch for this PR.
	log.Info("checking out branch %q", p.Branch)
	checkoutCmd := exec.Command("git", "checkout", p.Branch) // #nosec
	checkoutCmd.Dir = cloneDir
	if err := checkoutCmd.Run(); err != nil {
		return "", errors.Wrapf(err, "checking out branch %s", p.Branch)
	}
	return cloneDir, nil
}

// GetWorkingDir returns the path to the workspace for this repo and pull.
func (w *FileWorkspace) GetWorkingDir(r models.Repo, p models.PullRequest, workspace string) (string, error) {
	repoDir := w.cloneDir(r, p, workspace)
	if _, err := os.Stat(repoDir); err != nil {
		return "", errors.Wrap(err, "checking if workspace exists")
	}
	return repoDir, nil
}

// Delete deletes the workspace for this repo and pull.
func (w *FileWorkspace) Delete(r models.Repo, p models.PullRequest) error {
	return os.RemoveAll(w.repoPullDir(r, p))
}

// Delete deletes the working dir for this workspace.
func (w *FileWorkspace) DeleteForWorkspace(r models.Repo, p models.PullRequest, workspace string) error {
	return os.RemoveAll(w.cloneDir(r, p, workspace))
}

func (w *FileWorkspace) repoPullDir(r models.Repo, p models.PullRequest) string {
	return filepath.Join(w.DataDir, workingDirPrefix, r.FullName, strconv.Itoa(p.Num))
}

func (w *FileWorkspace) cloneDir(r models.Repo, p models.PullRequest, workspace string) string {
	return filepath.Join(w.repoPullDir(r, p), workspace)
}
