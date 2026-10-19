// Command rekey-gupy-boards is a one-off: moves every gupy board from its numeric companyId
// to the career-page subdomains its postings live on, and moves the stored postings with it.
//
// WHY. Gupy's companyId-keyed listing (employability-portal.gupy.io/api/v1/jobs) began
// answering an nginx 404 to every query on 2026-09-30. All 1,511 gupy boards failed every
// crawl from then on, and since a failing board is never swept, 73,156 postings stayed open
// with last_seen_at frozen at that day (measured 2026-10-19). The listing that still works is
// the one every career page renders from — job-publication/public/jobs?subdomain=<page> — and
// nothing public lists by companyId any more, so the board key has to change. One company
// commonly owns several career pages (an internal-mobility page, a confidential one): on prod
// 595 boards mapped to one page and 916 to two or more.
//
// WHERE THE MAPPING COMES FROM. Every stored posting's URL names the career page it was
// published on (https://<page>.gupy.io/job/...), so the companyId -> pages mapping is read
// from the catalogue itself, with no request to Gupy.
//
// WHAT IT WRITES, per companyId board, in ONE transaction:
//  1. each posting's external_id "<companyId>:<job id>" becomes "<page>:<job id>" — the row
//     keeps its id, so saves, tracking, enrichment and views follow it, and the first crawl of
//     the new board UPDATES the posting instead of inserting a duplicate beside it;
//  2. one board per page (status 'pending', company copied from the old board), through
//     boardcatalog's Inserter — a page a previous board already created is skipped;
//  3. the companyId board is retired.
//
// The order inside the transaction is what makes a crawl racing the run harmless: a new board
// is not visible to the crawler until its postings already carry its namespace. A companyId
// board is retired even when none of its postings names a page — a numeric key can never be
// crawled again — and is listed in the report so a curator can re-add the company by page.
//
// WHAT CLOSES THE STALE POSTINGS. Nothing here closes anything. gupy is now a
// fullBoardListing adapter, so the first successful crawl of each new board closes, through
// the ordinary board-scoped sweep, every posting of that page the listing no longer carries —
// all of them last seen 2026-09-30, far past the 48h grace. Postings on a page that no longer
// exists (Gupy answers 404 "Career Page not Found") are left to close-chronic-boards.
//
// Reports by default; --apply writes. Idempotent: a moved board is retired, so a re-run reads
// only what is left. Needs only DATABASE_URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/strelov1/freehire/internal/ingest/boardcatalog"
	"github.com/strelov1/freehire/internal/ingest/sources"
	"github.com/strelov1/freehire/internal/platform/db"
	"github.com/strelov1/freehire/internal/platform/externalid"
	"github.com/strelov1/freehire/internal/platform/worker"
)

const provider = "gupy"

func main() { worker.Main(run) }

func run() int {
	apply := flag.Bool("apply", false, "actually write; without it the run only reports")
	flag.Parse()

	ctx, _, pool, cleanup, err := worker.Bootstrap(context.Background())
	if err != nil {
		log.Printf("database: %v", err)
		return 1
	}
	defer cleanup()

	if err := rekey(ctx, pool, *apply); err != nil {
		log.Printf("rekey-gupy-boards: %v", err)
		return 1
	}
	return 0
}

// totals is the run's report.
type totals struct {
	boards, moved, pagesAdded, pagesExisting, postings, unplaced int
	noPages                                                      []string
}

func rekey(ctx context.Context, pool *pgxpool.Pool, apply bool) error {
	boards, err := boardcatalog.NewQueriesRepository(db.New(pool)).ListActiveForProvider(ctx, provider)
	if err != nil {
		return fmt.Errorf("list boards: %w", err)
	}
	registry := sources.Taxonomy()

	var t totals
	for _, b := range boards {
		if !isCompanyIDBoard(b.Board) {
			continue
		}
		rows, err := db.New(pool).ListBoardJobURLs(ctx, db.ListBoardJobURLsParams{
			Source: provider, BoardPattern: externalid.BoardPattern(b.Board),
		})
		if err != nil {
			return fmt.Errorf("board %s: list postings: %w", b.Board, err)
		}
		plan := planBoard(b.Board, rows)
		if plan.alreadyMoved {
			continue
		}
		t.boards++
		t.postings += len(plan.ids)
		t.unplaced += plan.unplaced
		if len(plan.pages) == 0 {
			t.noPages = append(t.noPages, fmt.Sprintf("%s (%s)", b.Board, b.Company))
		}
		log.Printf("board %s (%s): %d postings -> pages [%s], %d unplaced",
			b.Board, b.Company, len(plan.ids), strings.Join(plan.pages, " "), plan.unplaced)
		if !apply {
			continue
		}
		added, existing, err := moveBoard(ctx, pool, registry, b, plan)
		if err != nil {
			return fmt.Errorf("board %s: %w", b.Board, err)
		}
		t.moved++
		t.pagesAdded += added
		t.pagesExisting += existing
	}

	log.Printf("companyId boards: %d; postings re-keyed: %d; postings naming no page: %d",
		t.boards, t.postings, t.unplaced)
	if len(t.noPages) > 0 {
		log.Printf("boards with no career page in the catalogue (retired, re-add by page if wanted): %s",
			strings.Join(t.noPages, ", "))
	}
	if !apply {
		log.Print("dry run, nothing written. Re-run with --apply to move the boards.")
		return nil
	}
	log.Printf("moved %d boards: %d career-page boards added, %d already existed", t.moved, t.pagesAdded, t.pagesExisting)
	return nil
}

// moveBoard applies one board's plan in a single transaction: re-key its postings, add a
// board per career page, retire the companyId board. It returns how many page boards it added
// and how many already existed.
func moveBoard(ctx context.Context, pool *pgxpool.Pool, registry map[string]sources.Source,
	b boardcatalog.Board, plan boardPlan) (added, existing int, err error) {
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if len(plan.ids) > 0 {
			if _, err := q.RekeyJobExternalIDs(ctx, db.RekeyJobExternalIDsParams{
				Source: provider, Ids: plan.ids, ExternalIds: plan.externalIDs,
			}); err != nil {
				return fmt.Errorf("re-key postings: %w", err)
			}
		}
		repo := boardcatalog.NewQueriesRepository(q)
		inserter := boardcatalog.NewInserter(repo, registry)
		for _, page := range plan.pages {
			nb, err := inserter.Insert(ctx, boardcatalog.InsertInput{
				Provider: provider, Board: page, Region: b.Region, Company: b.Company,
			}, boardcatalog.StatusPending)
			switch {
			case errors.Is(err, boardcatalog.ErrDuplicateBoard):
				existing++
			case err != nil:
				return fmt.Errorf("add page %s: %w", page, err)
			case nb.Status == boardcatalog.StatusRejected:
				return fmt.Errorf("add page %s: rejected: %s", page, nb.RejectedReason)
			default:
				added++
			}
		}
		if _, err := repo.Retire(ctx, provider, b.Board, b.Region); err != nil {
			return fmt.Errorf("retire: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return added, existing, nil
}
