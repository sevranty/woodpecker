// Copyright 2026 Woodpecker Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build test

package queue

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

func TestPersistentSQLiteLeaseRetryBudgetSurvivesRestartAndTerminates(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "lease-budget.sqlite")

	s, closeStore := openQueueSQLiteStore(t, ctx, path)
	q, memory, stop := newSQLitePersistentQueue(t, ctx, s)
	task := createQueueSQLiteWorkflow(t, s, model.StatusRunning)
	require.NoError(t, q.PushAtOnce(ctx, []*model.Task{task}))

	first, err := q.Poll(ctx, 1, filterFnTrue)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 1, first.LeaseAttempts)
	expireQueueSQLiteTask(t, ctx, memory, task.ID)
	requireQueueSQLiteBackupCount(t, s, 1)

	stop()
	closeStore()
	s, _ = openQueueSQLiteStore(t, ctx, path)
	q, memory, _ = newSQLitePersistentQueue(t, ctx, s)
	require.Equal(t, 1, q.Info(ctx).Stats.Pending)

	second, err := q.Poll(ctx, 2, filterFnTrue)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, 2, second.LeaseAttempts)
	expireQueueSQLiteTask(t, ctx, memory, task.ID)
	requireQueueSQLiteBackupCount(t, s, 1)

	third, err := q.Poll(ctx, 3, filterFnTrue)
	require.NoError(t, err)
	require.NotNil(t, third)
	require.Equal(t, 3, third.LeaseAttempts)

	memory.Lock()
	current := memory.running[task.ID]
	require.NotNil(t, current)
	current.deadline = time.Now().Add(-time.Second)
	memory.Unlock()
	select {
	case <-current.done:
		require.ErrorIs(t, current.error, ErrTaskExpired)
	case <-ctx.Done():
		t.Fatal("expired lease did not reach terminal state")
	}

	requireQueueSQLiteEmpty(t, ctx, q, s)
	workflow, err := s.WorkflowLoad(1)
	require.NoError(t, err)
	require.Equal(t, model.StatusFailure, workflow.State)
	require.True(t, strings.Contains(workflow.Error, "3 assignments"), workflow.Error)
}
