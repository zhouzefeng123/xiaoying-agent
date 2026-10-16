package cmd

import "testing"

func TestParseRAGEvalCaseLineArrow(t *testing.T) {
	item, err := parseRAGEvalCaseLine("redis endpoint 是什么=>redis://localhost:6379")
	if err != nil {
		t.Fatalf("parse case: %v", err)
	}
	if item.Query != "redis endpoint 是什么" {
		t.Fatalf("unexpected query: %q", item.Query)
	}
	if item.Expected != "redis://localhost:6379" {
		t.Fatalf("unexpected expected: %q", item.Expected)
	}
}

func TestParseRAGEvalCaseLineTab(t *testing.T) {
	item, err := parseRAGEvalCaseLine("默认回答风格\t简洁||先给结论")
	if err != nil {
		t.Fatalf("parse case: %v", err)
	}
	if item.Query != "默认回答风格" {
		t.Fatalf("unexpected query: %q", item.Query)
	}
	if item.Expected != "简洁||先给结论" {
		t.Fatalf("unexpected expected: %q", item.Expected)
	}
}

func TestSplitExpectedNeedles(t *testing.T) {
	needles := splitExpectedNeedles("简洁||先给结论")
	if len(needles) != 2 {
		t.Fatalf("expected 2 needles, got %d", len(needles))
	}
	if needles[0] != "简洁" || needles[1] != "先给结论" {
		t.Fatalf("unexpected needles: %#v", needles)
	}
}
