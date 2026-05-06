package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/admin/pending"
)

// Pending handles `alf pending [list]`. Read-only enumeration of the
// admin-side ratification queue. Does NOT require a TTY — listing is
// safe to pipe into other tools.
//
// The current surface is a single subcommand (`list`, default). A
// `--detail <id>` view + JSON output mode are tracked as follow-ups
// when the agent → Runtime → Append plumbing lands and the queue
// grows beyond a handful of items.
func Pending(env Env, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			args = args[1:]
		case "-h", "--help":
			return printPendingUsage(env.Stdout)
		default:
			return fmt.Errorf("pending: unknown subcommand %q", args[0])
		}
	}
	if len(args) > 0 {
		return fmt.Errorf("pending list: unexpected argument %q", args[0])
	}

	store, err := pending.NewDirStore(env.PendingDir, env.Now)
	if err != nil {
		return fmt.Errorf("pending: open queue: %w", err)
	}
	items, err := store.List(context.Background())
	if err != nil {
		return fmt.Errorf("pending: list: %w", err)
	}

	if len(items) == 0 {
		fmt.Fprintln(env.Stdout, "No pending ratifications.")
		fmt.Fprintf(env.Stdout, "Queue dir: %s\n", store.Dir())
		return nil
	}

	now := env.Now()
	fmt.Fprintf(env.Stdout, "%-14s  %-18s  %-9s  %-22s  %s\n", "ID", "KIND", "AGE", "FROM", "PAYLOAD")
	for _, it := range items {
		from := string(it.CreatedBy)
		if from == "" {
			from = "<daemon>"
		}
		age := durationShort(now.Sub(it.CreatedAt))
		fmt.Fprintf(env.Stdout, "%-14s  %-18s  %-9s  %-22s  %s\n",
			it.ID, it.Kind, age, truncate(from, 22), payloadOneLine(it.Payload))
	}
	fmt.Fprintf(env.Stdout, "\nApprove or deny with: alf ratify <id> [--deny]\n")
	fmt.Fprintf(env.Stdout, "Queue dir: %s\n", store.Dir())
	return nil
}

// Ratify handles `alf ratify <id> [--deny]`. Default action is approve;
// --deny removes the item without acting on it. Both require a TTY +
// explicit "yes" confirmation — non-TTY input is the prompt-injection
// signature this boundary blocks.
//
// Approving an item ONLY removes it from the queue. The actual side
// effect (e.g. trust.add, bundle install) is the responsibility of
// whichever consumer Append'd the item — chunk 3 ships the queue
// machinery; the agent → Runtime → Append plumbing will follow when
// a widening-capable cap lands. For the soak window, the operator
// can still test the round-trip via `alf admin enqueue-test` once
// that helper exists, or via direct Append from a unit test.
func Ratify(env Env, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("ratify: missing <id> argument")
	}
	deny := false
	id := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--deny":
			deny = true
		case "-h", "--help":
			return printRatifyUsage(env.Stdout)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("ratify: unknown flag %q", args[i])
			}
			if id != "" {
				return fmt.Errorf("ratify: only one <id> argument allowed")
			}
			id = args[i]
		}
	}
	if id == "" {
		return fmt.Errorf("ratify: missing <id> argument")
	}

	if env.IsTerminal != nil && !env.IsTerminal() {
		return ErrNonInteractive
	}

	store, err := pending.NewDirStore(env.PendingDir, env.Now)
	if err != nil {
		return fmt.Errorf("ratify: open queue: %w", err)
	}

	// Show the operator what they're about to act on.
	items, err := store.List(context.Background())
	if err != nil {
		return fmt.Errorf("ratify: list: %w", err)
	}
	var target *pending.Item
	for i := range items {
		if items[i].ID == id {
			target = &items[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("ratify: no pending item with id %s in %s", id, store.Dir())
	}

	verb := "Approve"
	if deny {
		verb = "Deny"
	}
	fmt.Fprintf(env.Stdout, "%s pending item %s ?\n", verb, target.ID)
	fmt.Fprintf(env.Stdout, "  Kind:    %s\n", target.Kind)
	fmt.Fprintf(env.Stdout, "  From:    %s\n", nonEmpty(string(target.CreatedBy), "<daemon>"))
	fmt.Fprintf(env.Stdout, "  Created: %s\n", target.CreatedAt.Format(time.RFC3339))
	for _, k := range sortedKeys(target.Payload) {
		fmt.Fprintf(env.Stdout, "  %s: %s\n", k, target.Payload[k])
	}

	prompt := "Type 'yes' to approve: "
	if deny {
		prompt = "Type 'yes' to deny: "
	}
	if err := requireConfirm(env, prompt); err != nil {
		return err
	}

	var (
		out pending.Item
	)
	if deny {
		out, err = store.Deny(context.Background(), id)
	} else {
		out, err = store.Approve(context.Background(), id)
	}
	if err != nil {
		if errors.Is(err, pending.ErrNotFound) {
			return fmt.Errorf("ratify: item %s already removed (race)", id)
		}
		return fmt.Errorf("ratify: %w", err)
	}

	if deny {
		fmt.Fprintf(env.Stdout, "Denied %s (%s).\n", out.ID, out.Kind)
	} else {
		fmt.Fprintf(env.Stdout, "Approved %s (%s).\n", out.ID, out.Kind)
		fmt.Fprintln(env.Stdout, "Note: removal from the queue does NOT itself execute the requested operation.")
		fmt.Fprintln(env.Stdout, "The consumer that Append'd this item is responsible for the actual effect.")
	}
	return nil
}

// payloadOneLine renders the payload map as "k=v k=v" sorted by key.
// Truncated at 60 chars for the table view.
func payloadOneLine(p map[string]string) string {
	if len(p) == 0 {
		return ""
	}
	keys := sortedKeys(p)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(p[k])
	}
	return truncate(b.String(), 60)
}

func sortedKeys(p map[string]string) []string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// durationShort renders a duration like "2h", "3d", "12s" — always
// the largest unit that fits, integer-only. Used to keep the pending
// table compact.
func durationShort(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func printPendingUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, `Usage: alf pending [list]

List items in the admin-side ratification queue. Read-only.

The queue lives at <dataDir>/admin/pending/ as one JSON file per
item (mode 0600). Items appear here when a capability prepares an
operation that requires explicit operator approval (trust.add,
bundle.install, permission.widen).

Use 'alf ratify <id>' to approve or 'alf ratify <id> --deny' to
deny.`)
	return err
}

func printRatifyUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, `Usage: alf ratify <id> [--deny]

Approve (default) or deny a pending ratification item.

Approving an item removes it from the queue, signalling the consumer
that Append'd it that the operation may proceed. The actual side
effect is the consumer's responsibility — alf ratify only flips the
gate.

Refuses on non-TTY stdin and prompts for explicit 'yes' confirm.`)
	return err
}
