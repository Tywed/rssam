package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"rssam/internal/auth"

	"rssam/internal/opml"
)

type importJobStatus string

const (
	importJobPending importJobStatus = "pending"
	importJobRunning importJobStatus = "running"
	importJobDone    importJobStatus = "done"
	importJobFailed  importJobStatus = "failed"
)

type importJob struct {
	ID        string
	UserID    int64
	Status    importJobStatus
	Total     int
	Processed int
	Report    importReportDTO
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type importJobManager struct {
	mu   sync.RWMutex
	jobs map[string]*importJob
}

func newImportJobManager() *importJobManager {
	return &importJobManager{jobs: make(map[string]*importJob)}
}

func (m *importJobManager) create(userID int64, total int) *importJob {
	id := newImportJobID()
	now := time.Now().UTC()
	j := &importJob{
		ID:        id,
		UserID:    userID,
		Status:    importJobPending,
		Total:     total,
		CreatedAt: now,
		UpdatedAt: now,
		Report:    importReportDTO{Errors: make([]importErrorDTO, 0)},
	}
	m.mu.Lock()
	cutoff := now.Add(-2 * time.Hour)
	for jobID, job := range m.jobs {
		if job.UpdatedAt.Before(cutoff) {
			delete(m.jobs, jobID)
		}
	}
	m.jobs[id] = j
	m.mu.Unlock()
	return j
}

// get returns a copy: the import goroutine keeps mutating the stored job.
func (m *importJobManager) get(userID int64, id string) (importJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok || j.UserID != userID {
		return importJob{}, false
	}
	return *j, true
}

func (m *importJobManager) update(id string, fn func(*importJob)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return
	}
	fn(j)
	j.UpdatedAt = time.Now().UTC()
}

func newImportJobID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type importJobDTO struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	Total       int              `json:"total"`
	Processed   int              `json:"processed"`
	ProgressPct float64          `json:"progress_pct"`
	Report      *importReportDTO `json:"report,omitempty"`
	Error       string           `json:"error,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func toImportJobDTO(j *importJob) importJobDTO {
	pct := 0.0
	if j.Total > 0 {
		pct = float64(j.Processed) / float64(j.Total) * 100
	}
	if j.Status == importJobDone {
		pct = 100
	}
	dto := importJobDTO{
		ID:          j.ID,
		Status:      string(j.Status),
		Total:       j.Total,
		Processed:   j.Processed,
		ProgressPct: pct,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
	}
	if j.Error != "" {
		dto.Error = j.Error
	}
	if j.Status == importJobDone || j.Status == importJobFailed {
		report := j.Report
		dto.Report = &report
	}
	return dto
}

func (s *Server) handleGetImportJob(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, auth.RoleEditor, s.importJobs != nil, "import jobs are not configured")
	if !ok {
		return
	}
	jobID := r.PathValue("jobID")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id is required")
		return
	}
	j, ok := s.importJobs.get(p.UserID, jobID)
	if !ok {
		writeError(w, http.StatusNotFound, "import job not found")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[importJobDTO]{Data: toImportJobDTO(&j), Total: 1})
}

func (s *Server) startAsyncOPMLImport(r *http.Request, doc *opml.Document, userID int64) string {
	entries := doc.CollectFeeds()
	limit := s.maxImportFeeds
	if limit <= 0 {
		limit = 500
	}
	total := min(len(entries), limit)
	j := s.importJobs.create(userID, total)
	// The request context is cancelled as soon as 202 is written; the job
	// keeps the principal and request metadata but not the cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), asyncImportTimeout)
	detached := r.WithContext(ctx)
	go func() {
		defer cancel()
		s.importJobs.update(j.ID, func(job *importJob) {
			job.Status = importJobRunning
		})
		report := s.importOPML(detached, doc, userID, j.ID)
		s.importJobs.update(j.ID, func(job *importJob) {
			job.Status = importJobDone
			if ctx.Err() != nil {
				job.Status = importJobFailed
				job.Error = ctx.Err().Error()
			}
			job.Processed = job.Total
			job.Report = report
		})
	}()
	return j.ID
}

// asyncImportTimeout bounds one background OPML import (500 feeds with
// title discovery stay well inside it).
const asyncImportTimeout = 10 * time.Minute
