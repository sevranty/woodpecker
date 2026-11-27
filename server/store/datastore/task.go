// Copyright 2021 Woodpecker Authors
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
	"errors"
	"fmt"

	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

func (s storage) TaskList() ([]*model.Task, error) {
	tasks := make([]*model.Task, 0, perPage)
	return tasks, s.engine.Find(&tasks)
}

func (s storage) TaskInsert(task *model.Task) error {
	// only Insert set auto created ID back to object
	return wrapInsert(s.engine.Insert(task))
}

func (s storage) TaskDelete(id string) error {
	return retryWrite(func() error {
		return wrapDelete(s.engine.Where("id = ?", id).Delete(new(model.Task)))
	})
}

// TaskInsertBatch persists an entire queue admission in one transaction.
// It deliberately does not publish or claim tasks. The caller must not enqueue
// any of them until the transaction has committed. A commit error must be
// reconciled by the caller before retrying; its outcome may be unknown.
func (s storage) TaskInsertBatch(tasks []*model.Task) error {
	if len(tasks) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task == nil || task.ID == "" {
			return errors.New("task batch contains a nil task or empty ID")
		}
		if _, exists := seen[task.ID]; exists {
			return errors.New("task batch contains a duplicate ID")
		}
		seen[task.ID] = struct{}{}
	}

	sess := s.engine.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer func() { _ = sess.Rollback() }()

	for _, task := range tasks {
		copyTask := *task
		count, err := sess.Insert(&copyTask)
		if err := wrapInsert(count, err); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("task batch insert affected %d rows, expected one", count)
		}
	}
	return sess.Commit()
}
