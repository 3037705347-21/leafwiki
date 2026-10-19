// White-box test: package tagmerge so the real handler methods run.
package tagmerge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	coretagmerge "github.com/perber/wiki/internal/tagmerge"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestRouter(t *testing.T, job *MergeJob, trigger func()) (*gin.Engine, *coretagmerge.Service) {
	t.Helper()
	svc, err := coretagmerge.New(t.TempDir())
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	routes := NewRoutes(RoutesConfig{
		GetRules:    NewGetRulesUseCase(svc),
		UpdateRules: NewUpdateRulesUseCase(svc),
		Apply:       NewTriggerApplyUseCase(svc, job, trigger),
		Status:      NewGetStatusUseCase(job),
	})
	r := gin.New()
	r.GET("/rules", routes.handleGetRules)
	r.PUT("/rules", routes.handleUpdateRules)
	r.POST("/apply", routes.handleApply)
	r.GET("/status", routes.handleStatus)
	return r, svc
}

func putRules(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/rules", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestRules_RoundTrip(t *testing.T) {
	r, _ := newTestRouter(t, NewMergeJob(), func() {})

	w := putRules(t, r, `{"rules":[{"canonical":"JavaScript","aliases":["JS","js"]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", w.Code, w.Body.String())
	}
	var out RulesOutput
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Rules) != 1 || out.Rules[0].Canonical != "javascript" || len(out.Rules[0].Aliases) != 1 {
		t.Fatalf("normalized rules = %#v", out.Rules)
	}

	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/rules", nil)
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET status = %d", w2.Code)
	}
}

func TestRules_PutInvalidBody(t *testing.T) {
	r, _ := newTestRouter(t, NewMergeJob(), func() {})
	w := putRules(t, r, `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestApply_ConflictReturns409AndDoesNotStart(t *testing.T) {
	job := NewMergeJob()
	triggered := false
	r, _ := newTestRouter(t, job, func() { triggered = true })

	w := putRules(t, r, `{"rules":[
		{"canonical":"javascript","aliases":["js"]},
		{"canonical":"frontend","aliases":["js"]}
	]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d body=%s", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/apply", nil)
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusConflict {
		t.Fatalf("apply status = %d, want 409", w2.Code)
	}
	var body tagMergeErrorResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != ErrCodeConflict {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if triggered {
		t.Fatal("trigger must not run for conflicting rules")
	}
	if st := job.Status(); st.Running || st.Done {
		t.Fatalf("job must stay idle, got %#v", st)
	}
}

func TestApply_AcceptsCleanRulesAndStarts(t *testing.T) {
	job := NewMergeJob()
	launched := make(chan struct{}, 1)
	r, _ := newTestRouter(t, job, func() { launched <- struct{}{} })

	w := putRules(t, r, `{"rules":[{"canonical":"javascript","aliases":["js"]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d", w.Code)
	}

	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/apply", nil)
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("apply status = %d body=%s", w2.Code, w2.Body.String())
	}
	<-launched

	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest(http.MethodPost, "/apply", nil)
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusConflict {
		t.Fatalf("second apply status = %d, want 409", w3.Code)
	}
}

func TestStatus_CountersSerialize(t *testing.T) {
	job := NewMergeJob()
	job.Start()
	job.SetPhase(PhaseApply)
	job.SetTotalPages(3)
	job.AddScanned(3)
	job.AddChanged(2)
	job.AddUnsupported(1)

	r, _ := newTestRouter(t, job, func() {})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/status", nil)
	r.ServeHTTP(w, req)

	var st JobStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !st.Running || st.Phase != "apply" || st.TotalPages != 3 ||
		st.ScannedPages != 3 || st.ChangedPages != 2 || st.UnsupportedPages != 1 {
		t.Fatalf("status = %#v", st)
	}
}
