package identity

import (
	"context"
	"testing"
)

// The resolution ORDER is the whole contract.
func TestAuthorResolutionOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("explicit wins over everything", func(t *testing.T) {
		r := New("from-config", func(context.Context) string { return "from-lookup" })
		if got := r.Author(ctx, "  jsyoon "); got != "jsyoon" {
			t.Fatalf("Author = %q — a named author must never be overridden", got)
		}
	})

	t.Run("configured beats the lookup", func(t *testing.T) {
		r := New("jsmith", func(context.Context) string { return "from-lookup" })
		if got := r.Author(ctx, ""); got != "jsmith" {
			t.Fatalf("Author = %q", got)
		}
	})

	t.Run("an injected lookup beats git and the OS user", func(t *testing.T) {
		t.Setenv("USER", "dev")
		r := New("", func(context.Context) string { return "forge-name" })
		if got := r.Author(ctx, ""); got != "forge-name" {
			t.Fatalf("Author = %q", got)
		}
	})

	t.Run("a lookup that cannot say falls through", func(t *testing.T) {
		t.Setenv("USER", "dev")
		t.Setenv("LOGNAME", "")
		t.Setenv("USERNAME", "")
		// Force git out of the picture so the OS user is the next step.
		t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
		t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		r := New("", func(context.Context) string { return "" })
		if got := r.Author(ctx, ""); got != "dev" {
			t.Fatalf("Author = %q, want the OS user", got)
		}
	})
}

// A whitespace-only argument is not an author. Without the trim it would win
// over the configured value and stamp a blank name on the revision.
func TestBlankExplicitAuthorFallsThrough(t *testing.T) {
	r := New("jsmith", nil)
	if got := r.Author(context.Background(), "   "); got != "jsmith" {
		t.Fatalf("Author = %q", got)
	}
}

// A revision must never be written with a blank byline.
func TestAuthorIsNeverEmpty(t *testing.T) {
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "")
	t.Setenv("USERNAME", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := New("", func(context.Context) string { return "" })
	if got := r.Author(context.Background(), ""); got != Fallback {
		t.Fatalf("Author = %q, want %q", got, Fallback)
	}
}

// The lookup is memoized: a session that writes ten documents must not repeat
// it ten times, and the answer must not change between calls.
func TestLookupRunsOnce(t *testing.T) {
	calls := 0
	r := New("", func(context.Context) string { calls++; return "once" })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if got := r.Author(ctx, ""); got != "once" {
			t.Fatalf("call %d = %q", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("lookup ran %d times, want 1", calls)
	}
}
