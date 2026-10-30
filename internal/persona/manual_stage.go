package persona

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

type manualOutputKey struct{}
type manualTurnKey struct{}
type manualTurnOutcome struct{ err error }

func manualOutputID(ctx context.Context) string {
	id, _ := ctx.Value(manualOutputKey{}).(string)
	return id
}

func observeManualTurn(ctx context.Context) (context.Context, *manualTurnOutcome) {
	result := &manualTurnOutcome{}
	return context.WithValue(ctx, manualTurnKey{}, result), result
}

func manualTurnFailed(ctx context.Context, err error) {
	if result, ok := ctx.Value(manualTurnKey{}).(*manualTurnOutcome); ok && result.err == nil {
		result.err = err
	}
}

type manualStageRecord struct {
	Request string `json:"request"`
	Done    bool   `json:"done"`
}

func manualHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// manualStage holds a renewable lock while working and persists completion
// only after success. A published output or durable external ask also acts as
// a commit record if the worker dies before saving the completion checkpoint.
// Keep one record per mode/persona, rather than one per click.
func (r *Runner) manualStage(ctx context.Context, period, id string, request time.Time, work func(context.Context) error) error {
	key := manualHash(manualStageID(period, id))
	ctx, unlock, err := r.store.Lock(ctx, "manual-check-in:"+key)
	if err != nil {
		return err
	}
	defer unlock()
	stamp := manualStamp(request)
	output := "e_manual_" + manualHash(key+":"+stamp)
	ctx = context.WithValue(ctx, manualOutputKey{}, output)
	var state manualStageRecord
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.store.UpdateRecord(ctx, store.RecordCheckIns, key, time.Time{}, func(raw []byte, exists bool) ([]byte, error) {
		state = manualStageRecord{}
		if exists {
			if err := json.Unmarshal(raw, &state); err != nil {
				return nil, err
			}
		}
		if state.Request != stamp {
			state = manualStageRecord{Request: stamp}
		}
		return json.Marshal(state)
	}); err != nil {
		return err
	}
	if state.Done {
		return nil
	}
	_, published := r.store.Entry(output)
	_, handedOff, err := r.store.Record(ctx, store.RecordAsks, "ask_"+output)
	if err != nil {
		return err
	}
	if !published && !handedOff {
		if err := work(ctx); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.store.UpdateRecord(ctx, store.RecordCheckIns, key, time.Time{}, func(raw []byte, exists bool) ([]byte, error) {
		var current manualStageRecord
		if !exists {
			return nil, errors.New("manual check-in checkpoint disappeared")
		}
		if err := json.Unmarshal(raw, &current); err != nil {
			return nil, err
		}
		if current.Request != stamp {
			return nil, errors.New("manual check-in checkpoint changed")
		}
		current.Done = true
		return json.Marshal(current)
	})
}
