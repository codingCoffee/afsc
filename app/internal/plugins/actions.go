package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"afsc/internal/jobs"
)

const (
	// ActionKind is the job that runs one plugin's handlers for one event.
	ActionKind     = "plugins.action"
	actionTimeout  = 30 * time.Second
	actionAttempts = 3
)

type action struct {
	plugin    string
	prio, seq int
	fn        func(context.Context, json.RawMessage) error
}

// AddAction subscribes fn to e. Handlers run in a job after the change commits, at least once, so they must be
// idempotent on the envelope ID. An error retries this plugin's handlers for the event (3 attempts).
func AddAction[D any](h *Host, e Event[D], prio int, fn func(ctx context.Context, ev Envelope[D]) error) {
	h.addAction(e.Name, prio, func(ctx context.Context, raw json.RawMessage) error {
		var ev Envelope[D]
		if err := json.Unmarshal(raw, &ev); err != nil {
			return err
		}
		return fn(ctx, ev)
	})
}

// AddAllActions subscribes fn to every event, with the data left as raw JSON.
func AddAllActions(h *Host, prio int, fn func(ctx context.Context, ev Envelope[json.RawMessage]) error) {
	for _, name := range EventNames {
		AddAction(h, Event[json.RawMessage]{name}, prio, fn)
	}
}

func (h *Host) addAction(name string, prio int, fn func(context.Context, json.RawMessage) error) {
	if !slices.Contains(EventNames, name) {
		h.fail("unknown event %q", name)
		return
	}
	*h.seq++
	h.rt.actions[name] = append(h.rt.actions[name], action{plugin: h.id, prio: prio, seq: *h.seq, fn: fn})
}

// subscribers lists the enabled plugins with a handler for event, once each.
func (rt *Runtime) subscribers(event string) []string {
	var out []string
	for _, a := range rt.actions[event] {
		if !slices.Contains(out, a.plugin) && rt.Enabled(a.plugin) {
			out = append(out, a.plugin)
		}
	}
	return out
}

// Emit queues e for every enabled subscriber in db, which should be the transaction making the change, so a
// rolled-back change fires nothing. A nil runtime or no subscribers writes nothing.
func Emit[D any](ctx context.Context, rt *Runtime, db jobs.Execer, e Event[D], data D) error {
	if rt == nil {
		return nil
	}
	subs := rt.subscribers(e.Name)
	if len(subs) == 0 {
		return nil
	}
	b, err := json.Marshal(Envelope[D]{ID: uuid.New(), Event: e.Name, OccurredAt: time.Now().UTC(), Data: data})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
INSERT INTO jobs (kind, payload, max_attempts)
SELECT $1, jsonb_build_object('plugin', p, 'envelope', $2::jsonb), $3 FROM unnest($4::text[]) AS p`,
		ActionKind, string(b), actionAttempts, subs)
	return err
}

type actionJob struct {
	Plugin   string          `json:"plugin"`
	Envelope json.RawMessage `json:"envelope"`
}

func (rt *Runtime) runAction(ctx context.Context, payload json.RawMessage) error {
	var j actionJob
	if err := json.Unmarshal(payload, &j); err != nil {
		return err
	}
	if !rt.Enabled(j.Plugin) {
		return nil
	}
	var head struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(j.Envelope, &head); err != nil {
		return err
	}
	for _, a := range rt.actions[head.Event] {
		if a.plugin != j.Plugin {
			continue
		}
		_, err := call(ctx, actionTimeout, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, a.fn(ctx, j.Envelope)
		})
		if err != nil {
			rt.logFailure(a.plugin, head.Event, err)
			return fmt.Errorf("%s: %s: %w", a.plugin, head.Event, err)
		}
	}
	return nil
}
