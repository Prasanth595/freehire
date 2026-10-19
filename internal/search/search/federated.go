package search

import "github.com/meilisearch/meilisearch-go"

// buildFederatedSearch turns a q with several OR alternatives into ONE federated
// multi-search over the jobs index. Each alternative carries the caller's filter,
// sort and vector; federation pages, deduplicates and ranks the merged list. The
// per-query limit/offset are cleared because Meilisearch rejects them inside a
// federation, which is where paging lives.
func buildFederatedSearch(p SearchParams, alts []Alternative) *meilisearch.MultiSearchRequest {
	queries := make([]*meilisearch.SearchRequest, 0, len(alts))
	for _, a := range alts {
		q := p
		q.Query = a.Text
		if a.Fields != nil {
			q.QFields = a.Fields
		}
		req := buildSearchRequest(q)
		req.IndexUID = facetIndexUID
		req.Query = a.Text
		req.Limit, req.Offset = 0, 0
		queries = append(queries, req)
	}
	return &meilisearch.MultiSearchRequest{
		Federation: &meilisearch.MultiSearchFederation{Limit: int64(p.Limit), Offset: int64(p.Offset)},
		Queries:    queries,
	}
}
