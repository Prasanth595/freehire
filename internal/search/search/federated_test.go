package search

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/meilisearch/meilisearch-go"
)

func TestFederatedSearchCarriesFilterSortAndFieldsPerQuery(t *testing.T) {
	p := SearchParams{Query: "ignored", Filter: "source = \"x\"", Sort: []string{"posted_at:desc"}, Limit: 25, Offset: 50, QFields: []string{"company"}}
	req := buildFederatedSearch(p, []Alternative{{Text: "produktchef", Fields: []string{"title"}}, {Text: "\"product owner\""}})

	if req.Federation == nil || req.Federation.Limit != 25 || req.Federation.Offset != 50 {
		t.Fatalf("Federation = %+v, want limit 25 offset 50", req.Federation)
	}
	if len(req.Queries) != 2 {
		t.Fatalf("got %d queries, want 2", len(req.Queries))
	}
	for i, q := range req.Queries {
		if q.IndexUID != facetIndexUID {
			t.Errorf("query %d IndexUID = %q, want %q", i, q.IndexUID, facetIndexUID)
		}
		if q.Filter != p.Filter || !reflect.DeepEqual(q.Sort, p.Sort) {
			t.Errorf("query %d lost filter/sort: %+v", i, q)
		}
		if q.Limit != 0 || q.Offset != 0 {
			t.Errorf("query %d carries limit/offset (%d/%d); federation rejects that", i, q.Limit, q.Offset)
		}
	}
	if req.Queries[0].Query != "produktchef" || !reflect.DeepEqual(req.Queries[0].AttributesToSearchOn, []string{"title"}) {
		t.Errorf("query 0 = %q on %v, want produktchef on [title]", req.Queries[0].Query, req.Queries[0].AttributesToSearchOn)
	}
	if req.Queries[1].Query != "\"product owner\"" || !reflect.DeepEqual(req.Queries[1].AttributesToSearchOn, []string{"company"}) {
		t.Errorf("query 1 = %q on %v, want the q_fields fallback [company]", req.Queries[1].Query, req.Queries[1].AttributesToSearchOn)
	}
}

func TestSearchWithoutOperatorsSendsTodaysSingleRequest(t *testing.T) {
	var gotQuery string
	c := &Client{
		facet: fakeSearchIndex{searchFn: func(_ context.Context, q string, _ *meilisearch.SearchRequest) (*meilisearch.SearchResponse, error) {
			gotQuery = q
			return &meilisearch.SearchResponse{}, nil
		}},
		manager: fakeManager{multiFn: func(context.Context, *meilisearch.MultiSearchRequest) (*meilisearch.MultiSearchResponse, error) {
			t.Fatal("a query without operators must not go federated")
			return nil, nil
		}},
	}
	if _, err := c.Search(context.Background(), SearchParams{Query: "golang  rust", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "golang  rust" {
		t.Fatalf("query = %q, want q untouched", gotQuery)
	}
}

func TestSearchSingleTitleAlternativeStaysSingleIndex(t *testing.T) {
	var got *meilisearch.SearchRequest
	var gotQuery string
	c := &Client{facet: fakeSearchIndex{searchFn: func(_ context.Context, q string, r *meilisearch.SearchRequest) (*meilisearch.SearchResponse, error) {
		gotQuery, got = q, r
		return &meilisearch.SearchResponse{}, nil
	}}}
	if _, err := c.Search(context.Background(), SearchParams{Query: "title:\"product owner\"", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "\"product owner\"" || !reflect.DeepEqual(got.AttributesToSearchOn, []string{"title"}) {
		t.Fatalf("sent %q on %v, want \"product owner\" on [title]", gotQuery, got.AttributesToSearchOn)
	}
}

func TestSearchWithOrGoesFederated(t *testing.T) {
	var got *meilisearch.MultiSearchRequest
	c := &Client{manager: fakeManager{multiFn: func(_ context.Context, r *meilisearch.MultiSearchRequest) (*meilisearch.MultiSearchResponse, error) {
		got = r
		return &meilisearch.MultiSearchResponse{EstimatedTotalHits: 7}, nil
	}}}
	res, err := c.Search(context.Background(), SearchParams{Query: "golang OR rust", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Queries) != 2 || res.Total != 7 {
		t.Fatalf("request = %+v, total = %d; want 2 federated queries and total 7", got, res.Total)
	}
}

func TestSearchRefusesTooManyAlternatives(t *testing.T) {
	c := &Client{}
	_, err := c.Search(context.Background(), SearchParams{Query: "a OR b OR c OR d OR e OR f", Limit: 10})
	if !errors.Is(err, ErrTooManyAlternatives) {
		t.Fatalf("err = %v, want ErrTooManyAlternatives", err)
	}
}
