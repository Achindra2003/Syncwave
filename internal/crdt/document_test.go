package crdt

import (
	"strings"
	"testing"
)

// ---------- Basic Operations ----------

func TestNewDocument(t *testing.T) {
	doc := NewDocument()
	if doc.Len() != 0 {
		t.Errorf("new doc: expected len 0, got %d", doc.Len())
	}
	if doc.String() != "" {
		t.Errorf("new doc: expected empty string, got %q", doc.String())
	}
}

func TestInsertSingle(t *testing.T) {
	doc := NewDocument()
	doc.Insert('A', RootID, OpID{Clock: 1, SiteID: "u1"})

	if doc.String() != "A" {
		t.Errorf("expected %q, got %q", "A", doc.String())
	}
	if doc.Len() != 1 {
		t.Errorf("expected len 1, got %d", doc.Len())
	}
}

func TestInsertSequence(t *testing.T) {
	doc := NewDocument()
	doc.Insert('H', RootID, OpID{1, "u1"})
	doc.Insert('i', OpID{1, "u1"}, OpID{2, "u1"})
	doc.Insert('!', OpID{2, "u1"}, OpID{3, "u1"})

	want := "Hi!"
	if doc.String() != want {
		t.Errorf("expected %q, got %q", want, doc.String())
	}
}

func TestDelete(t *testing.T) {
	doc := NewDocument()
	id := OpID{1, "u1"}
	doc.Insert('X', RootID, id)
	doc.Delete(id)

	if doc.String() != "" {
		t.Errorf("expected empty after delete, got %q", doc.String())
	}
	if doc.Len() != 0 {
		t.Errorf("expected len 0 after delete, got %d", doc.Len())
	}
}

func TestDeleteMiddle(t *testing.T) {
	doc := NewDocument()
	doc.Insert('A', RootID, OpID{1, "u1"})
	doc.Insert('B', OpID{1, "u1"}, OpID{2, "u1"})
	doc.Insert('C', OpID{2, "u1"}, OpID{3, "u1"})

	doc.Delete(OpID{2, "u1"}) // delete 'B'

	if doc.String() != "AC" {
		t.Errorf("expected %q, got %q", "AC", doc.String())
	}
}

func TestIdempotentInsert(t *testing.T) {
	doc := NewDocument()
	id := OpID{1, "u1"}
	doc.Insert('A', RootID, id)
	doc.Insert('A', RootID, id) // duplicate — should be ignored

	if doc.String() != "A" {
		t.Errorf("idempotent insert failed: expected %q, got %q", "A", doc.String())
	}
}

// ---------- RGA Tie-Breaking ----------

func TestConcurrentInsertsSamePosition(t *testing.T) {
	doc := NewDocument()
	// Two users insert at root with the same clock value.
	// RGA tie-breaking: higher SiteID wins priority.
	doc.Insert('A', RootID, OpID{1, "user1"})
	doc.Insert('B', RootID, OpID{1, "user2"})

	// "user2" > "user1" lexicographically, so B has higher priority → comes first
	want := "BA"
	if doc.String() != want {
		t.Errorf("concurrent tie-break: expected %q, got %q", want, doc.String())
	}
}

func TestConcurrentInsertsDifferentClocks(t *testing.T) {
	doc := NewDocument()
	// Higher clock always wins regardless of SiteID.
	doc.Insert('X', RootID, OpID{2, "aaa"})
	doc.Insert('Y', RootID, OpID{3, "aaa"})

	// Clock 3 > 2, so Y has higher priority → comes first
	want := "YX"
	if doc.String() != want {
		t.Errorf("expected %q, got %q", want, doc.String())
	}
}

// ---------- Position Mapping ----------

func TestGetNodeIDAtVisiblePos(t *testing.T) {
	doc := NewDocument()
	doc.Insert('A', RootID, OpID{1, "x"})
	doc.Insert('B', OpID{1, "x"}, OpID{2, "x"})
	doc.Insert('C', OpID{2, "x"}, OpID{3, "x"})

	tests := []struct {
		pos       int
		wantClock int64
	}{
		{0, 1}, // 'A'
		{1, 2}, // 'B'
		{2, 3}, // 'C'
	}

	for _, tt := range tests {
		id := doc.GetNodeIDAtVisiblePos(tt.pos)
		if id.Clock != tt.wantClock {
			t.Errorf("pos %d: expected clock %d, got %d", tt.pos, tt.wantClock, id.Clock)
		}
	}
}

func TestGetVisiblePosOfNode(t *testing.T) {
	doc := NewDocument()
	doc.Insert('A', RootID, OpID{1, "x"})
	doc.Insert('B', OpID{1, "x"}, OpID{2, "x"})
	doc.Insert('C', OpID{2, "x"}, OpID{3, "x"})

	tests := []struct {
		id      OpID
		wantPos int
	}{
		{OpID{1, "x"}, 0},
		{OpID{2, "x"}, 1},
		{OpID{3, "x"}, 2},
		{OpID{99, "z"}, -1}, // non-existent
	}

	for _, tt := range tests {
		pos := doc.GetVisiblePosOfNode(tt.id)
		if pos != tt.wantPos {
			t.Errorf("node {%d,%s}: expected pos %d, got %d",
				tt.id.Clock, tt.id.SiteID, tt.wantPos, pos)
		}
	}
}

func TestGetAnchorIDForPos(t *testing.T) {
	doc := NewDocument()
	doc.Insert('A', RootID, OpID{1, "x"})
	doc.Insert('B', OpID{1, "x"}, OpID{2, "x"})

	// Anchor for pos 0 is RootID (insert before 'A')
	anchor0 := doc.GetAnchorIDForPos(0)
	if anchor0 != RootID {
		t.Errorf("anchor for pos 0: expected RootID, got {%d,%s}", anchor0.Clock, anchor0.SiteID)
	}

	// Anchor for pos 1 is 'A' (insert after 'A')
	anchor1 := doc.GetAnchorIDForPos(1)
	if anchor1.Clock != 1 {
		t.Errorf("anchor for pos 1: expected clock 1, got %d", anchor1.Clock)
	}
}

func TestGetVisibleNodeIDs(t *testing.T) {
	doc := NewDocument()
	doc.Insert('A', RootID, OpID{1, "x"})
	doc.Insert('B', OpID{1, "x"}, OpID{2, "x"})
	doc.Delete(OpID{1, "x"}) // delete 'A'

	ids := doc.GetVisibleNodeIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 visible node, got %d", len(ids))
	}
	if ids[0].Clock != 2 {
		t.Errorf("expected clock 2, got %d", ids[0].Clock)
	}
}

// ---------- Full Document Build ----------

func TestBuildDocument(t *testing.T) {
	doc := NewDocument()
	text := "Hello, World!"

	var prev OpID = RootID
	for i, ch := range text {
		id := OpID{Clock: int64(i + 1), SiteID: "A"}
		doc.Insert(ch, prev, id)
		prev = id
	}

	if doc.String() != text {
		t.Errorf("expected %q, got %q", text, doc.String())
	}
	if doc.Len() != len(text) {
		t.Errorf("expected len %d, got %d", len(text), doc.Len())
	}
}

// ---------- Table-Driven Scenario Tests ----------

func TestScenarios(t *testing.T) {
	type op struct {
		action   string // "insert" or "delete"
		char     rune
		anchorID OpID
		id       OpID
	}

	tests := []struct {
		name     string
		ops      []op
		expected string
	}{
		{
			name: "insert at beginning then end",
			ops: []op{
				{"insert", 'B', RootID, OpID{1, "u1"}},
				{"insert", 'A', RootID, OpID{2, "u1"}},           // before B (higher clock wins)
				{"insert", 'C', OpID{1, "u1"}, OpID{3, "u1"}},    // after B
			},
			expected: "ABC",
		},
		{
			name: "delete then reinsert at same position",
			ops: []op{
				{"insert", 'X', RootID, OpID{1, "u1"}},
				{"delete", 0, OpID{}, OpID{1, "u1"}},
				{"insert", 'Y', RootID, OpID{2, "u1"}},
			},
			expected: "Y",
		},
		{
			name: "multi-user interleaved",
			ops: []op{
				{"insert", 'A', RootID, OpID{1, "alice"}},
				{"insert", 'B', RootID, OpID{1, "bob"}}, // same clock, "bob" > "alice"
				{"insert", 'C', OpID{1, "alice"}, OpID{2, "alice"}},
			},
			expected: "BAC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := NewDocument()
			for _, o := range tt.ops {
				switch o.action {
				case "insert":
					doc.Insert(o.char, o.anchorID, o.id)
				case "delete":
					doc.Delete(o.id)
				}
			}
			if doc.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, doc.String())
			}
		})
	}
}

// ---------- Stress Test ----------

func TestLargeDocument(t *testing.T) {
	doc := NewDocument()
	n := 1000
	var prev OpID = RootID

	for i := 0; i < n; i++ {
		id := OpID{Clock: int64(i + 1), SiteID: "stress"}
		doc.Insert('x', prev, id)
		prev = id
	}

	if doc.Len() != n {
		t.Errorf("expected len %d, got %d", n, doc.Len())
	}
	if doc.String() != strings.Repeat("x", n) {
		t.Error("large document content mismatch")
	}
}

// ---------- FindNode ----------

func TestFindNode(t *testing.T) {
	doc := NewDocument()
	id := OpID{1, "u1"}
	doc.Insert('A', RootID, id)

	if doc.FindNode(id) == nil {
		t.Error("FindNode returned nil for existing node")
	}
	if doc.FindNode(OpID{99, "z"}) != nil {
		t.Error("FindNode returned non-nil for missing node")
	}
}
