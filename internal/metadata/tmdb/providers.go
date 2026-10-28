package tmdb

import (
	"context"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// A streaming service is often several TMDB watch providers: its plans
// ("Paramount Plus Essential", "Paramount Plus Premium"), its channels on
// other stores ("Paramount+ Amazon Channel") and older entries that no
// longer list anything. A query for one of them asks for them all.

// serviceNames names the services whose TMDB IDs have moved or split, so
// they still match when the region no longer lists the saved ID.
var serviceNames = map[int][]string{
	8:    {"netflix"},
	9:    {"amazon prime video"},
	119:  {"amazon prime video"},
	337:  {"disney plus"},
	1899: {"max", "hbo max"},
	384:  {"hbo max", "max"},
	15:   {"hulu"},
	350:  {"apple tv plus"},
	531:  {"paramount plus"},
	386:  {"peacock"},
	387:  {"peacock"},
}

// providerName lowercases a provider's name and spells "+" out, so
// "Paramount+" and "Paramount Plus" read the same.
func providerName(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "+", " plus "))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}), " ")
}

// sameService says whether name is the service or one of its plans or
// channels: it starts with the service's name as whole words.
func sameService(name, service string) bool {
	return name == service || strings.HasPrefix(name, service+" ")
}

// RegionProviders lists every streaming service, for movies or shows, in
// the client's region.
func (c *Client) RegionProviders(ctx context.Context) ([]WatchProvider, error) {
	var all []WatchProvider
	seen := map[int]bool{}
	var firstErr error
	for _, kind := range []string{"movie", "tv"} {
		var r struct {
			Results []WatchProvider `json:"results"`
		}
		if err := c.get(ctx, "/watch/providers/"+kind, url.Values{"watch_region": {c.Region()}}, 7*24*time.Hour, &r); err != nil {
			firstErr = err
			continue
		}
		for _, p := range r.Results {
			if !seen[p.ID] {
				seen[p.ID] = true
				all = append(all, p)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Priority < all[j].Priority })
	if len(all) == 0 {
		return nil, firstErr
	}
	return all, nil
}

// ServiceProviders is every provider in the region that's one of ids'
// services, ids first.
func (c *Client) ServiceProviders(ctx context.Context, ids []int) []WatchProvider {
	ps, _ := c.RegionProviders(ctx)
	var names []string
	for _, id := range ids {
		names = append(names, serviceNames[id]...)
		for _, p := range ps {
			if p.ID == id {
				names = append(names, providerName(p.Name))
			}
		}
	}
	var out []WatchProvider
	for _, id := range ids {
		if i := slices.IndexFunc(ps, func(p WatchProvider) bool { return p.ID == id }); i >= 0 {
			out = append(out, ps[i])
		} else {
			out = append(out, WatchProvider{ID: id})
		}
	}
	var more []WatchProvider
	for _, p := range ps {
		if slices.ContainsFunc(out, func(o WatchProvider) bool { return o.ID == p.ID }) {
			continue
		}
		n := providerName(p.Name)
		if !strings.HasSuffix(n, " store") && slices.ContainsFunc(names, func(s string) bool { return sameService(n, s) }) {
			more = append(more, p)
		}
	}
	// The service itself before its plans, and those before its channels
	// on other stores.
	rank := func(p WatchProvider) int {
		switch n := providerName(p.Name); {
		case slices.Contains(names, n):
			return 0
		case strings.Contains(n, "channel"):
			return 2
		}
		return 1
	}
	sort.SliceStable(more, func(i, j int) bool { return rank(more[i]) < rank(more[j]) })
	return append(out, more...)
}

// serviceIDs is ServiceProviders' IDs.
func (c *Client) serviceIDs(ctx context.Context, ids []int) []int {
	ps := c.ServiceProviders(ctx, ids)
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}
