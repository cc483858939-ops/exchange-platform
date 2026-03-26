package tasks

import (
	"fmt"
	"testing"

	"Go.exchange/consts"
	"Go.exchange/models"
)

// --- 测试辅助 ---

// fakeRedisState 模拟一个内存中的 redis 状态，方便测试断言
type fakeRedisState struct {
	dirtySet      map[string]struct{}
	processingSet map[string]struct{}
	deadLetter    map[string]struct{}
	retryCounts   map[string]int64
}

func newFakeRedisState(processingIDs ...string) *fakeRedisState {
	s := &fakeRedisState{
		dirtySet:      map[string]struct{}{},
		processingSet: map[string]struct{}{},
		deadLetter:    map[string]struct{}{},
		retryCounts:   map[string]int64{},
	}
	for _, id := range processingIDs {
		s.processingSet[id] = struct{}{}
	}
	return s
}

// setupMocks 替换所有可 mock 的函数变量，返回恢复函数
func setupMocks(
	s *fakeRedisState,
	batchErr error,
	singleErrFn func(id string) error,
) func() {
	origBatch := batchUpsert
	origSingle := singleUpsert
	origIncrRetry := incrRetryCount
	origMoveDL := moveToDeadLetter
	origRollback := rollbackToRetry
	origAck := ackSuccess

	batchUpsert = func(articles []models.Article) error {
		return batchErr
	}

	singleUpsert = func(article models.Article) error {
		idStr := fmt.Sprintf("%d", article.ID)
		return singleErrFn(idStr)
	}

	incrRetryCount = func(idStr string) (int64, error) {
		s.retryCounts[idStr]++
		return s.retryCounts[idStr], nil
	}

	moveToDeadLetter = func(ids ...interface{}) error {
		for _, id := range ids {
			idStr := fmt.Sprintf("%v", id)
			s.deadLetter[idStr] = struct{}{}
			delete(s.processingSet, idStr)
		}
		return nil
	}

	rollbackToRetry = func(ids ...interface{}) error {
		for _, id := range ids {
			idStr := fmt.Sprintf("%v", id)
			s.dirtySet[idStr] = struct{}{}
			delete(s.processingSet, idStr)
		}
		return nil
	}

	ackSuccess = func(ids ...interface{}) error {
		for _, id := range ids {
			idStr := fmt.Sprintf("%v", id)
			delete(s.processingSet, idStr)
		}
		return nil
	}

	return func() {
		batchUpsert = origBatch
		singleUpsert = origSingle
		incrRetryCount = origIncrRetry
		moveToDeadLetter = origMoveDL
		rollbackToRetry = origRollback
		ackSuccess = origAck
	}
}

// buildData 生成 processBatchData 需要的 []interface{} 格式
func buildData(pairs ...string) []interface{} {
	result := make([]interface{}, 0, len(pairs))
	for _, p := range pairs {
		result = append(result, p)
	}
	return result
}

// --- 测试用例 ---

// TestProcessBatchData_AllSuccess 验证批量全部成功时 processingSet 被清理
func TestProcessBatchData_AllSuccess(t *testing.T) {
	s := newFakeRedisState("1", "2")
	// 模拟 processingSet 里有 1, 2
	s.processingSet["1"] = struct{}{}
	s.processingSet["2"] = struct{}{}

	restore := setupMocks(s, nil, func(id string) error { return nil })
	defer restore()

	data := buildData("1", "100", "2", "200")
	processBatchData(data)

	if len(s.processingSet) != 0 {
		t.Errorf("批量成功后 processingSet 应为空，实际剩余: %v", s.processingSet)
	}
	if len(s.dirtySet) != 0 {
		t.Errorf("批量成功不应有数据回到 dirtySet，实际: %v", s.dirtySet)
	}
	if len(s.deadLetter) != 0 {
		t.Errorf("批量成功不应有数据进死信，实际: %v", s.deadLetter)
	}
}

// TestProcessBatchData_BatchFailFallbackToSingle 批量失败降级为逐条：单条失败则进重试队列，单条成功则 ACK
func TestProcessBatchData_BatchFailFallbackToSingle(t *testing.T) {
	s := newFakeRedisState("1", "2")

	// id=1 单条也失败，id=2 单条成功
	restore := setupMocks(s, fmt.Errorf("batch error"), func(id string) error {
		if id == "1" {
			return fmt.Errorf("single error for id=1")
		}
		return nil
	})
	defer restore()

	data := buildData("1", "100", "2", "200")
	processBatchData(data)

	// id=2 成功，应从 processingSet 移除
	if _, exists := s.processingSet["2"]; exists {
		t.Errorf("id=2 单条成功，应从 processingSet 移除")
	}
	// id=1 失败，应回到 dirtySet
	if _, exists := s.dirtySet["1"]; !exists {
		t.Errorf("id=1 单条失败（第一次），应回到 dirtySet，实际: %v", s.dirtySet)
	}
	// 重试次数为 1
	if s.retryCounts["1"] != 1 {
		t.Errorf("id=1 重试次数应为 1，实际: %d", s.retryCounts["1"])
	}
	// 不应进死信
	if _, exists := s.deadLetter["1"]; exists {
		t.Errorf("id=1 第一次失败，不应进死信")
	}
}

// TestProcessBatchData_ExceedMaxRetry 超过最大重试次数后进死信
func TestProcessBatchData_ExceedMaxRetry(t *testing.T) {
	s := newFakeRedisState("1")
	// 预置重试次数已达 MaxRetryCount-1，下一次失败就会进死信
	s.retryCounts["1"] = int64(consts.MaxRetryCount - 1)

	restore := setupMocks(s, fmt.Errorf("batch error"), func(id string) error {
		return fmt.Errorf("single error for id=%s", id)
	})
	defer restore()

	data := buildData("1", "100")
	processBatchData(data)

	// id=1 应进死信
	if _, exists := s.deadLetter["1"]; !exists {
		t.Errorf("id=1 超过最大重试次数，应进入死信集合，实际 deadLetter: %v", s.deadLetter)
	}
	// 不应回到 dirtySet
	if _, exists := s.dirtySet["1"]; exists {
		t.Errorf("id=1 已进死信，不应再回 dirtySet")
	}
	// processingSet 应清理
	if _, exists := s.processingSet["1"]; exists {
		t.Errorf("id=1 进死信后，processingSet 应清理")
	}
}
