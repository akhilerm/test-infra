/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mungers

import (
	"fmt"
	"time"

	"k8s.io/test-infra/mungegithub/features"
	"k8s.io/test-infra/mungegithub/github"
	"k8s.io/test-infra/mungegithub/mungeopts"
	"k8s.io/test-infra/mungegithub/options"

	"github.com/golang/glog"
	githubapi "github.com/google/go-github/github"
)

const (
	staleGreenCIHours = 96
	greenMsgFormat    = `@` + jenkinsBotName + ` test this

Tests are more than %d hours old. Re-running tests.`
)

var greenMsgBody = fmt.Sprintf(greenMsgFormat, staleGreenCIHours)

// StaleGreenCI will re-run passed tests for LGTM PRs if they are more than
// 96 hours old.
type StaleGreenCI struct {
	getRetestContexts func() []string
	features          *features.Features
}

func init() {
	s := &StaleGreenCI{}
	RegisterMungerOrDie(s)
	RegisterStaleIssueComments(s)
}

// Name is the name usable in --pr-mungers
func (s *StaleGreenCI) Name() string { return "stale-green-ci" }

// RequiredFeatures is a slice of 'features' that must be provided
func (s *StaleGreenCI) RequiredFeatures() []string { return []string{} }

// Initialize will initialize the munger
func (s *StaleGreenCI) Initialize(config *github.Config, features *features.Features) error {
	s.features = features
	s.getRetestContexts = func() []string {
		return mungeopts.RequiredContexts.Retest
	}
	return nil
}

// EachLoop is called at the start of every munge loop
func (s *StaleGreenCI) EachLoop() error { return nil }

// RegisterOptions registers config options for this munger.
func (s *StaleGreenCI) RegisterOptions(opts *options.Options) {}

// Munge is the workhorse the will actually make updates to the PR
func (s *StaleGreenCI) Munge(obj *github.MungeObject) {
	requiredContexts := s.getRetestContexts()
	if !obj.IsPR() {
		return
	}

	if !obj.HasLabel(lgtmLabel) {
		return
	}

	if obj.HasLabel(retestNotRequiredLabel) || obj.HasLabel(retestNotRequiredDocsOnlyLabel) {
		return
	}

	if mergeable, ok := obj.IsMergeable(); !mergeable || !ok {
		return
	}

	if success, ok := obj.IsStatusSuccess(requiredContexts); !success || !ok {
		return
	}

	for _, context := range requiredContexts {
		statusTime, ok := obj.GetStatusTime(context)
		if statusTime == nil || !ok {
			glog.Errorf("%d: unable to determine time %q context was set", *obj.Issue.Number, context)
			return
		}
		if time.Since(*statusTime) > staleGreenCIHours*time.Hour {
			err := obj.WriteComment(greenMsgBody)
			if err != nil {
				glog.Errorf("Failed to write retrigger old test comment")
				return
			}
			ok := obj.WaitForPending(requiredContexts)
			if !ok {
				glog.Errorf("Failed waiting for PR to start testing")
			}
			return
		}
	}
}

func (s *StaleGreenCI) isStaleIssueComment(obj *github.MungeObject, comment *githubapi.IssueComment) bool {
	if !mergeBotComment(comment) {
		return false
	}
	if *comment.Body != greenMsgBody {
		return false
	}
	stale := commentBeforeLastCI(obj, comment, mungeopts.RequiredContexts.Retest)
	if stale {
		glog.V(6).Infof("Found stale StaleGreenCI comment")
	}
	return stale
}

// StaleIssueComments returns a slice of stale issue comments.
func (s *StaleGreenCI) StaleIssueComments(obj *github.MungeObject, comments []*githubapi.IssueComment) []*githubapi.IssueComment {
	return forEachCommentTest(obj, comments, s.isStaleIssueComment)
}

func commentBeforeLastCI(obj *github.MungeObject, comment *githubapi.IssueComment, requiredContexts []string) bool {
	if success, ok := obj.IsStatusSuccess(requiredContexts); !success || !ok {
		return false
	}
	if comment.CreatedAt == nil {
		return false
	}
	commentTime := *comment.CreatedAt

	for _, context := range requiredContexts {
		statusTimeP, ok := obj.GetStatusTime(context)
		if statusTimeP == nil || !ok {
			return false
		}
		statusTime := statusTimeP.Add(30 * time.Minute)
		if commentTime.After(statusTime) {
			return false
		}
	}
	return true
}
