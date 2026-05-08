package hooks

import (
	"context"
	"slices"
	"testing"
)

func TestBusAppliesHooksDeterministically(t *testing.T) {
	bus := NewBus()
	order := []string{}
	bus.AddRunTransform("b", 20, func(_ context.Context, input RunContext) (RunContext, error) {
		order = append(order, "transform-b")
		return input, nil
	})
	bus.AddRunTransform("a", 20, func(_ context.Context, input RunContext) (RunContext, error) {
		order = append(order, "transform-a")
		return input, nil
	})
	bus.AddRunObserver("z", 30, func(_ context.Context, input RunContext) error {
		order = append(order, "observe-z")
		return nil
	})

	if _, _, _, err := bus.ApplyRun(context.Background(), RunContext{}); err != nil {
		t.Fatalf("apply run hooks: %v", err)
	}
	expected := []string{"transform-a", "transform-b", "observe-z"}
	if !slices.Equal(order, expected) {
		t.Fatalf("unexpected hook order: got=%v want=%v", order, expected)
	}
}
