//go:build integration

// Run with: go test -tags=integration ./cmd/rekey-gupy-boards/
// Requires Docker (testcontainers spins up a throwaway Postgres with the migrations).
package main

import (
	"context"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/strelov1/freehire/internal/ingest/boardcatalog"
	"github.com/strelov1/freehire/internal/ingest/sources"
	"github.com/strelov1/freehire/internal/platform/db"
	"github.com/strelov1/freehire/internal/platform/testdb"
)

func seedGupyJob(t *testing.T, pool *pgxpool.Pool, externalID, url string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO jobs (source, external_id, url, title, public_slug)
		 VALUES ('gupy', $1, $2, 'A job', 'job-' || replace($1, ':', '-')) RETURNING id`,
		externalID, url).Scan(&id); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	return id
}

func externalIDOf(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var ext string
	if err := pool.QueryRow(context.Background(), `SELECT external_id FROM jobs WHERE id = $1`, id).Scan(&ext); err != nil {
		t.Fatalf("read job %d: %v", id, err)
	}
	return ext
}

func liveBoards(t *testing.T, repo *boardcatalog.QueriesRepository) map[string]string {
	t.Helper()
	rows, err := repo.ListActiveForProvider(context.Background(), provider)
	if err != nil {
		t.Fatalf("ListActiveForProvider: %v", err)
	}
	out := map[string]string{}
	for _, b := range rows {
		out[b.Board] = b.Company
	}
	return out
}

func TestRekeyMovesBoardsAndPostingsToCareerPages(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Pool(t)
	repo := boardcatalog.NewQueriesRepository(db.New(pool))
	ins := boardcatalog.NewInserter(repo, sources.Taxonomy())
	for _, in := range []boardcatalog.InsertInput{
		{Provider: provider, Board: "48481", Company: "Órigo Energia"},
		{Provider: provider, Board: "137", Company: "Grupo Casas Bahia"},
		{Provider: provider, Board: "999", Company: "No Postings"},
		{Provider: provider, Board: "caju", Company: "Caju"}, // already a page board
	} {
		if _, err := ins.Insert(ctx, in, boardcatalog.StatusActive); err != nil {
			t.Fatalf("seed board %s: %v", in.Board, err)
		}
	}
	origo := seedGupyJob(t, pool, "48481:100", "https://origoenergia.gupy.io/job/abc?jobBoardSource=gupy_portal")
	interno := seedGupyJob(t, pool, "48481:101", "https://origoenergiainterno.gupy.io/jobs/101")
	// A page two companyId boards both published on: the second board finds it already added.
	shared1 := seedGupyJob(t, pool, "48481:102", "https://datalawyer.gupy.io/jobs/102")
	shared2 := seedGupyJob(t, pool, "137:200", "https://datalawyer.gupy.io/jobs/200")
	bahia := seedGupyJob(t, pool, "137:201", "https://lojasgrupocasasbahia.gupy.io/jobs/201")
	// A board whose id is a prefix of another's must not claim its postings.
	other := seedGupyJob(t, pool, "1370:300", "https://other.gupy.io/jobs/300")

	// A dry run writes nothing.
	if err := rekey(ctx, pool, false); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := externalIDOf(t, pool, origo); got != "48481:100" {
		t.Fatalf("dry run re-keyed a posting: %q", got)
	}

	if err := rekey(ctx, pool, true); err != nil {
		t.Fatalf("apply: %v", err)
	}

	for id, want := range map[int64]string{
		origo: "origoenergia:100", interno: "origoenergiainterno:101",
		shared1: "datalawyer:102", shared2: "datalawyer:200", bahia: "lojasgrupocasasbahia:201",
		other: "1370:300",
	} {
		if got := externalIDOf(t, pool, id); got != want {
			t.Errorf("job %d external_id = %q, want %q", id, got, want)
		}
	}

	boards := liveBoards(t, repo)
	want := map[string]string{
		"origoenergia": "Órigo Energia", "origoenergiainterno": "Órigo Energia",
		"datalawyer":           "Órigo Energia", // first board to name it keeps it
		"lojasgrupocasasbahia": "Grupo Casas Bahia", "caju": "Caju",
	}
	if len(boards) != len(want) {
		got := make([]string, 0, len(boards))
		for b := range boards {
			got = append(got, b)
		}
		sort.Strings(got)
		t.Fatalf("live boards = %v, want %v", got, want)
	}
	for b, company := range want {
		if boards[b] != company {
			t.Errorf("board %s company = %q, want %q", b, boards[b], company)
		}
	}

	// A re-run finds nothing left to move and changes nothing.
	if err := rekey(ctx, pool, true); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if got := len(liveBoards(t, repo)); got != len(want) {
		t.Errorf("re-run changed the live boards: %d, want %d", got, len(want))
	}
}
