// Copyright 2018 Drone.IO Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

func TestTaskList(t *testing.T) {
	store, closer := newTestStore(t, new(model.Task))
	defer closer()

	assert.NoError(t, store.TaskInsert(&model.Task{
		ID:        "some_random_id",
		Data:      []byte("foo"),
		Labels:    map[string]string{"foo": "bar"},
		DepStatus: map[string]model.StatusValue{"test": "dep"},
	}))

	list, err := store.TaskList()
	assert.NoError(t, err)
	assert.Len(t, list, 1, "Expected one task in list")
	assert.Equal(t, "some_random_id", list[0].ID)
	assert.Equal(t, "foo", string(list[0].Data))
	assert.EqualValues(t, map[string]model.StatusValue{"test": "dep"}, list[0].DepStatus)

	assert.NoError(t, store.TaskDelete("some_random_id"))

	list, err = store.TaskList()
	assert.NoError(t, err)
	assert.Len(t, list, 0, "Want empty task list after delete")
}

func TestTaskInsertBatch(t *testing.T) {
	store, closer := newTestStore(t, new(model.Task))
	defer closer()

	assert.NoError(t, store.TaskInsertBatch(nil))
	assert.Error(t, store.TaskInsertBatch([]*model.Task{{ID: "not-inserted"}, nil}))
	assert.Error(t, store.TaskInsertBatch([]*model.Task{{ID: ""}}))
	assert.Error(t, store.TaskInsertBatch([]*model.Task{{ID: "repeat"}, {ID: "repeat"}}))

	list, err := store.TaskList()
	require.NoError(t, err)
	require.Empty(t, list)

	require.NoError(t, store.TaskInsertBatch([]*model.Task{
		{ID: "first", Data: []byte("one")},
		{ID: "second", Data: []byte("two")},
	}))
	list, err = store.TaskList()
	require.NoError(t, err)
	require.Len(t, list, 2)
	data := make(map[string]string)
	for _, task := range list {
		data[task.ID] = string(task.Data)
	}
	assert.Equal(t, map[string]string{"first": "one", "second": "two"}, data)
}

func TestTaskInsertBatchRollback(t *testing.T) {
	store, closer := newTestStore(t, new(model.Task))
	defer closer()

	require.NoError(t, store.TaskInsert(&model.Task{ID: "existing", Data: []byte("keep")}))
	require.Error(t, store.TaskInsertBatch([]*model.Task{
		{ID: "before"},
		{ID: "existing", Data: []byte("replace")},
		{ID: "after"},
	}))

	list, err := store.TaskList()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "existing", list[0].ID)
	assert.Equal(t, "keep", string(list[0].Data))
}

func TestTaskInsertBatchCompetingAdmissions(t *testing.T) {
	store, closer := newTestStore(t, new(model.Task))
	defer closer()

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"left", "right"} {
		go func(id string) {
			<-start
			results <- store.TaskInsertBatch([]*model.Task{
				{ID: id},
				{ID: "shared"},
			})
		}(id)
	}
	close(start)
	first, second := <-results, <-results
	require.NotEqual(t, first == nil, second == nil, "exactly one competing batch commits")

	list, err := store.TaskList()
	require.NoError(t, err)
	require.Len(t, list, 2)
	ids := make(map[string]bool)
	for _, task := range list {
		ids[task.ID] = true
	}
	assert.True(t, ids["shared"])
	assert.NotEqual(t, ids["left"], ids["right"])
}

func TestTaskInsertBatchZeroRows(t *testing.T) {
	driver, _ := testDriverConfig()
	if driver != "sqlite3" {
		t.Skip("SQLite trigger fixture")
	}
	store, closer := newTestStore(t, new(model.Task))
	defer closer()

	_, err := store.engine.Exec(`CREATE TRIGGER ignore_task BEFORE INSERT ON tasks
		WHEN NEW.id = 'ignored' BEGIN SELECT RAISE(IGNORE); END`)
	require.NoError(t, err)
	require.Error(t, store.TaskInsertBatch([]*model.Task{
		{ID: "before"},
		{ID: "ignored"},
		{ID: "after"},
	}))
	list, err := store.TaskList()
	require.NoError(t, err)
	require.Empty(t, list)
}
