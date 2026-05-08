package hooks

import (
	"context"
	"sort"

	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

type BlockDecision struct {
	Blocked bool           `json:"blocked"`
	Reason  string         `json:"reason,omitempty"`
	Audit   map[string]any `json:"audit,omitempty"`
}

type RunContext struct {
	ProfileID  string
	SessionID  string
	RunID      string
	Request    providers.NormalizedRequest
	Resolution providers.ResolutionInput
}

type ToolContext struct {
	ProfileID  string
	SessionID  string
	RunID      string
	Surface    string
	Invocation tools.Invocation
}

type runTransform struct {
	id    string
	order int
	fn    func(context.Context, RunContext) (RunContext, error)
}

type runBlock struct {
	id    string
	order int
	fn    func(context.Context, RunContext) BlockDecision
}

type runObserve struct {
	id    string
	order int
	fn    func(context.Context, RunContext) error
}

type toolTransform struct {
	id    string
	order int
	fn    func(context.Context, ToolContext) (ToolContext, error)
}

type toolBlock struct {
	id    string
	order int
	fn    func(context.Context, ToolContext) BlockDecision
}

type toolObserve struct {
	id    string
	order int
	fn    func(context.Context, ToolContext) error
}

type Bus struct {
	runTransforms  []runTransform
	runBlocks      []runBlock
	runObservers   []runObserve
	toolTransforms []toolTransform
	toolBlocks     []toolBlock
	toolObservers  []toolObserve
}

func NewBus() *Bus {
	return &Bus{}
}

func (b *Bus) AddRunTransform(id string, order int, fn func(context.Context, RunContext) (RunContext, error)) {
	if b == nil || fn == nil {
		return
	}
	b.runTransforms = append(b.runTransforms, runTransform{id: id, order: order, fn: fn})
	sort.SliceStable(b.runTransforms, func(i, j int) bool {
		return hookLess(b.runTransforms[i].order, b.runTransforms[i].id, b.runTransforms[j].order, b.runTransforms[j].id)
	})
}

func (b *Bus) AddRunBlock(id string, order int, fn func(context.Context, RunContext) BlockDecision) {
	if b == nil || fn == nil {
		return
	}
	b.runBlocks = append(b.runBlocks, runBlock{id: id, order: order, fn: fn})
	sort.SliceStable(b.runBlocks, func(i, j int) bool {
		return hookLess(b.runBlocks[i].order, b.runBlocks[i].id, b.runBlocks[j].order, b.runBlocks[j].id)
	})
}

func (b *Bus) AddRunObserver(id string, order int, fn func(context.Context, RunContext) error) {
	if b == nil || fn == nil {
		return
	}
	b.runObservers = append(b.runObservers, runObserve{id: id, order: order, fn: fn})
	sort.SliceStable(b.runObservers, func(i, j int) bool {
		return hookLess(b.runObservers[i].order, b.runObservers[i].id, b.runObservers[j].order, b.runObservers[j].id)
	})
}

func (b *Bus) AddToolTransform(id string, order int, fn func(context.Context, ToolContext) (ToolContext, error)) {
	if b == nil || fn == nil {
		return
	}
	b.toolTransforms = append(b.toolTransforms, toolTransform{id: id, order: order, fn: fn})
	sort.SliceStable(b.toolTransforms, func(i, j int) bool {
		return hookLess(b.toolTransforms[i].order, b.toolTransforms[i].id, b.toolTransforms[j].order, b.toolTransforms[j].id)
	})
}

func (b *Bus) AddToolBlock(id string, order int, fn func(context.Context, ToolContext) BlockDecision) {
	if b == nil || fn == nil {
		return
	}
	b.toolBlocks = append(b.toolBlocks, toolBlock{id: id, order: order, fn: fn})
	sort.SliceStable(b.toolBlocks, func(i, j int) bool {
		return hookLess(b.toolBlocks[i].order, b.toolBlocks[i].id, b.toolBlocks[j].order, b.toolBlocks[j].id)
	})
}

func (b *Bus) AddToolObserver(id string, order int, fn func(context.Context, ToolContext) error) {
	if b == nil || fn == nil {
		return
	}
	b.toolObservers = append(b.toolObservers, toolObserve{id: id, order: order, fn: fn})
	sort.SliceStable(b.toolObservers, func(i, j int) bool {
		return hookLess(b.toolObservers[i].order, b.toolObservers[i].id, b.toolObservers[j].order, b.toolObservers[j].id)
	})
}

func (b *Bus) ApplyRun(ctx context.Context, input RunContext) (RunContext, string, BlockDecision, error) {
	current := input
	for _, hook := range b.runTransforms {
		next, err := hook.fn(ctx, current)
		if err != nil {
			return input, hook.id, BlockDecision{}, err
		}
		current = next
	}
	for _, hook := range b.runBlocks {
		decision := hook.fn(ctx, current)
		if decision.Blocked {
			if decision.Audit == nil {
				decision.Audit = map[string]any{}
			}
			decision.Audit["hook_id"] = hook.id
			return current, hook.id, decision, nil
		}
	}
	for _, hook := range b.runObservers {
		if err := hook.fn(ctx, current); err != nil {
			return input, hook.id, BlockDecision{}, err
		}
	}
	return current, "", BlockDecision{}, nil
}

func (b *Bus) ApplyTool(ctx context.Context, input ToolContext) (ToolContext, string, BlockDecision, error) {
	current := input
	for _, hook := range b.toolTransforms {
		next, err := hook.fn(ctx, current)
		if err != nil {
			return input, hook.id, BlockDecision{}, err
		}
		current = next
	}
	for _, hook := range b.toolBlocks {
		decision := hook.fn(ctx, current)
		if decision.Blocked {
			if decision.Audit == nil {
				decision.Audit = map[string]any{}
			}
			decision.Audit["hook_id"] = hook.id
			return current, hook.id, decision, nil
		}
	}
	for _, hook := range b.toolObservers {
		if err := hook.fn(ctx, current); err != nil {
			return input, hook.id, BlockDecision{}, err
		}
	}
	return current, "", BlockDecision{}, nil
}

func hookLess(leftOrder int, leftID string, rightOrder int, rightID string) bool {
	if leftOrder != rightOrder {
		return leftOrder < rightOrder
	}
	return leftID < rightID
}
