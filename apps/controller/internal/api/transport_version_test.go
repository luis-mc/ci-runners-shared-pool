package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// versioned is a reader that has a jobs version and counts its grouped reads.
type versioned struct {
	*fakeData
	ver    int64
	groups int
}

func (v *versioned) JobsVersion() int64 { return v.ver }

func (v *versioned) Groups(ctx context.Context, by jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	v.groups++
	return v.fakeData.Groups(ctx, by)
}

func getWith(mux *http.ServeMux, target, ifNoneMatch string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

// R3-8: a caller that holds the tag of the grouped table of the same jobs version gets a 304 without the read.
func TestGroupsNotModifiedSkipsTheRead(t *testing.T) {
	v := &versioned{fakeData: &fakeData{groups: []jobsdata.GroupRow{{Name: "test", Filter: "job_type=test", Done: 4}}}}
	mux := dataMux(v, auth.RoleViewer, nil)
	first := getWith(mux, "/api/v1/jobs/groups?by=job-type", "")
	tag := first.Header().Get("ETag")
	if first.Code != 200 || tag == "" || v.groups != 1 {
		t.Fatalf("first: %d tag %q reads %d", first.Code, tag, v.groups)
	}
	if rec := getWith(mux, "/api/v1/jobs/groups?by=job-type", tag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || v.groups != 1 || rec.Header().Get("ETag") != tag {
		t.Fatalf("same version: %d body %d reads %d", rec.Code, rec.Body.Len(), v.groups)
	}
	if getWith(mux, "/api/v1/jobs/groups?by=machine", tag); v.groups != 2 {
		t.Fatalf("another query must be read, reads %d", v.groups)
	}
	v.ver++ // a job was written
	getWith(mux, "/api/v1/jobs/groups?by=job-type", tag)
	if v.groups != 3 {
		t.Fatalf("a new version must read the groups again, reads %d", v.groups)
	}
	// A reader without a version keeps the old behaviour: the handler runs, the body tag still answers 304.
	plain := &fakeData{groups: v.fakeData.groups}
	pm := dataMux(plain, auth.RoleViewer, nil)
	t1 := getWith(pm, "/api/v1/jobs/groups?by=job-type", "").Header().Get("ETag")
	if rec := getWith(pm, "/api/v1/jobs/groups?by=job-type", t1); rec.Code != http.StatusNotModified {
		t.Fatalf("body tag: %d", rec.Code)
	}
}
