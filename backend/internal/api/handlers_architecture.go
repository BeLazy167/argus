package api

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

// Thresholds kept in sync with backend orchestrator / frontend.
const (
	// archChokePointFanIn is the minimum fan-in to qualify as a choke point.
	// Mirrors pipeline.ArchChokePointFanIn (verified in tests) to avoid drift.
	archChokePointFanIn = pipeline.ArchChokePointFanIn

	// weakCouplingCutoff drops file pairs with Jaccard < 0.3 to reduce graph noise.
	weakCouplingCutoff = 0.3
	// summaryCouplingCutoff is a tighter threshold for the top-level summary display.
	summaryCouplingCutoff = 0.5
	// tightCouplingThreshold marks "unstable coupling" insight for high-churn files.
	tightCouplingThreshold = 0.7
)

// ── Response types ──────────────────────────────────────────────────────

type archFile struct {
	Path            string        `json:"path"`
	Language        string        `json:"language"`
	Symbols         []string      `json:"symbols"`
	FanIn           int           `json:"fan_in"`
	FanOut          int           `json:"fan_out"`
	BugDensity      float64       `json:"bug_density"`
	ChangeFrequency int           `json:"change_frequency"`
	Coupling        []fileCoupling `json:"coupling"`
	RiskScore       float64       `json:"risk_score"`
	Percentiles     archPct       `json:"percentiles"`
	Insight         string        `json:"insight,omitempty"`
}

type fileCoupling struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

type archPct struct {
	FanIn           int `json:"fan_in"`
	BugDensity      int `json:"bug_density"`
	ChangeFrequency int `json:"change_frequency"`
	Coupling        int `json:"coupling"`
}

type archEdge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Kinds  []string `json:"kinds"`
	Weight int      `json:"weight"`
}

type archSummary struct {
	TotalFiles  int              `json:"total_files"`
	ChokePoints []string         `json:"choke_points"`
	Hotspots    []string         `json:"hotspots"`
	MostCoupled []coupledPair    `json:"most_coupled"`
}

type coupledPair struct {
	FileA string  `json:"file_a"`
	FileB string  `json:"file_b"`
	Score float64 `json:"score"`
}

type archResponse struct {
	Files             []archFile  `json:"files"`
	Edges             []archEdge  `json:"edges"`
	Summary           archSummary `json:"summary"`
	CouplingAvailable bool        `json:"coupling_available"`
}

// ── Handler ─────────────────────────────────────────────────────────────

func (s *Server) getArchitecture(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}

	ctx := r.Context()

	// All 4 queries are independent — run concurrently to cut latency.
	var (
		mu                sync.Mutex
		fileMap           = make(map[string]*archFileInfo)
		fanIn             = make(map[string]int)
		fanOut            = make(map[string]int)
		fileEdges         = make(map[string]*archEdgeAgg)
		bugCount          = make(map[string]int)
		changeFreq        = make(map[string]int)
		prFilesList       []archPRFiles
		couplingAvailable = true
		fetchErr          error
	)
	edgeKey := func(a, b string) string { return a + "\x00" + b }

	var wg sync.WaitGroup
	setErr := func(err error) {
		mu.Lock()
		if fetchErr == nil {
			fetchErr = err
		}
		mu.Unlock()
	}

	// Query 1: File-level nodes with symbols
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.fetchArchNodes(ctx, repoID, fileMap, &mu); err != nil {
			setErr(err)
		}
	}()

	// Query 2: Fan-in / fan-out at file level
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.fetchArchEdges(ctx, repoID, fanIn, fanOut, fileEdges, edgeKey, &mu); err != nil {
			setErr(err)
		}
	}()

	// Query 3: Bug density + change frequency
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.fetchArchBugs(ctx, repoID, bugCount, changeFreq, &mu); err != nil {
			setErr(err)
		}
	}()

	// Query 4: Coupling co-change
	wg.Add(1)
	go func() {
		defer wg.Done()
		list, err := s.fetchArchCoupling(ctx, repoID)
		if err != nil {
			s.logger.Warn("architecture: coupling query (non-fatal)", "error", err)
			mu.Lock()
			couplingAvailable = false
			mu.Unlock()
			return
		}
		mu.Lock()
		prFilesList = list
		mu.Unlock()
	}()

	wg.Wait()

	if fetchErr != nil {
		s.logger.Error("architecture: query failed", "error", fetchErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load architecture"})
		return
	}

	if len(fileMap) == 0 {
		writeJSON(w, http.StatusOK, archResponse{
			Files: []archFile{}, Edges: []archEdge{},
			Summary:           archSummary{},
			CouplingAvailable: couplingAvailable,
		})
		return
	}

	// Compute Jaccard: for each file pair, |intersection| / |union| of PRs
	coChange := make(map[string]map[string]int) // file -> file -> co-occurrence count
	fileOccurrence := make(map[string]int)       // file -> total PR count
	for _, pf := range prFilesList {
		for _, f := range pf.files {
			fileOccurrence[f]++
		}
		for i := 0; i < len(pf.files); i++ {
			for j := i + 1; j < len(pf.files); j++ {
				a, b := pf.files[i], pf.files[j]
				if a > b {
					a, b = b, a
				}
				if coChange[a] == nil {
					coChange[a] = make(map[string]int)
				}
				coChange[a][b]++
			}
		}
	}

	// Build coupling map: file -> top 3 coupled files with Jaccard score
	couplingMap := make(map[string][]fileCoupling)
	maxCoupling := make(map[string]float64) // max coupling score per file
	for a, partners := range coChange {
		for b, co := range partners {
			union := fileOccurrence[a] + fileOccurrence[b] - co
			if union == 0 {
				continue
			}
			score := float64(co) / float64(union)
			if score < weakCouplingCutoff {
				continue // ignore weak coupling
			}
			couplingMap[a] = append(couplingMap[a], fileCoupling{Path: b, Score: math.Round(score*100) / 100})
			couplingMap[b] = append(couplingMap[b], fileCoupling{Path: a, Score: math.Round(score*100) / 100})
			if score > maxCoupling[a] {
				maxCoupling[a] = score
			}
			if score > maxCoupling[b] {
				maxCoupling[b] = score
			}
		}
	}
	// Sort and cap at 3 per file
	for fp := range couplingMap {
		sort.Slice(couplingMap[fp], func(i, j int) bool {
			return couplingMap[fp][i].Score > couplingMap[fp][j].Score
		})
		if len(couplingMap[fp]) > 3 {
			couplingMap[fp] = couplingMap[fp][:3]
		}
	}

	files := make([]archFile, 0, len(fileMap))
	for fp, fi := range fileMap {
		linesApprox := fi.linesSum
		if linesApprox < 10 {
			linesApprox = 10 // floor to avoid division by tiny numbers
		}
		density := float64(bugCount[fp]) / float64(linesApprox) * 100
		density = math.Round(density*100) / 100

		af := archFile{
			Path:            fp,
			Language:        fi.language,
			Symbols:         fi.symbols,
			FanIn:           fanIn[fp],
			FanOut:          fanOut[fp],
			BugDensity:      density,
			ChangeFrequency: changeFreq[fp],
			Coupling:        couplingMap[fp],
		}
		if af.Coupling == nil {
			af.Coupling = []fileCoupling{}
		}
		if af.Symbols == nil {
			af.Symbols = []string{}
		}
		files = append(files, af)
	}

	// Sort by path for deterministic output
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	if len(files) > 0 {
		// Collect metric vectors
		fanIns := make([]float64, len(files))
		bugDens := make([]float64, len(files))
		chgFreqs := make([]float64, len(files))
		couplings := make([]float64, len(files))
		for i := range files {
			fanIns[i] = float64(files[i].FanIn)
			bugDens[i] = files[i].BugDensity
			chgFreqs[i] = float64(files[i].ChangeFrequency)
			couplings[i] = maxCoupling[files[i].Path]
		}

		wFanIn, wBug, wChg, wCoup := adaptiveWeights(fanIns, bugDens, chgFreqs, couplings)

		// Normalize each metric to 0-10 range
		maxFanIn := maxVal(fanIns)
		maxBug := maxVal(bugDens)
		maxChg := maxVal(chgFreqs)
		maxCoup := maxVal(couplings)

		for i := range files {
			normFI := safeNorm(float64(files[i].FanIn), maxFanIn) * 10
			normBD := safeNorm(files[i].BugDensity, maxBug) * 10
			normCF := safeNorm(float64(files[i].ChangeFrequency), maxChg) * 10
			normCP := safeNorm(maxCoupling[files[i].Path], maxCoup) * 10

			risk := wFanIn*normFI + wBug*normBD + wChg*normCF + wCoup*normCP
			files[i].RiskScore = math.Round(risk*10) / 10
		}

		computePercentiles(files, fanIns, bugDens, chgFreqs, couplings)

		p90FanIn := percentileValue(fanIns, 90)
		p90Bug := percentileValue(bugDens, 90)
		p90Chg := percentileValue(chgFreqs, 90)
		for i := range files {
			switch {
			case float64(files[i].FanIn) >= p90FanIn && files[i].BugDensity >= p90Bug:
				files[i].Insight = "Critical choke point with high bug density. " + strconv.Itoa(files[i].FanIn) + " files depend on this. Prioritize refactoring."
			case float64(files[i].FanIn) >= p90FanIn:
				files[i].Insight = "Choke point. " + strconv.Itoa(files[i].FanIn) + " files break if this breaks. Consider splitting into smaller modules."
			case files[i].BugDensity >= p90Bug:
				files[i].Insight = "Bug hotspot. High defect rate per line. Review changes here carefully."
			case float64(files[i].ChangeFrequency) >= p90Chg && maxCoupling[files[i].Path] >= tightCouplingThreshold:
				files[i].Insight = "Unstable coupling. Changes frequently and tightly coupled — changes here ripple."
			case float64(files[i].ChangeFrequency) >= p90Chg:
				files[i].Insight = "High churn. Frequently modified — may indicate unclear responsibilities."
			}
		}
	}

	edges := make([]archEdge, 0, len(fileEdges))
	for k, ea := range fileEdges {
		parts := splitEdgeKey(k)
		if len(parts) != 2 {
			continue
		}
		kinds := make([]string, 0, len(ea.kinds))
		for kind := range ea.kinds {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		edges = append(edges, archEdge{
			Source: parts[0],
			Target: parts[1],
			Kinds:  kinds,
			Weight: ea.count,
		})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].Source+edges[i].Target < edges[j].Source+edges[j].Target })

	summary := archSummary{TotalFiles: len(files)}

	// Sort by risk descending for choke points / hotspots
	byRisk := make([]archFile, len(files))
	copy(byRisk, files)
	sort.Slice(byRisk, func(i, j int) bool { return byRisk[i].RiskScore > byRisk[j].RiskScore })

	for _, f := range byRisk {
		if len(summary.ChokePoints) < 5 && f.FanIn >= archChokePointFanIn {
			summary.ChokePoints = append(summary.ChokePoints, f.Path)
		}
		if len(summary.Hotspots) < 5 && f.BugDensity > 0 {
			summary.Hotspots = append(summary.Hotspots, f.Path)
		}
	}
	if summary.ChokePoints == nil {
		summary.ChokePoints = []string{}
	}
	if summary.Hotspots == nil {
		summary.Hotspots = []string{}
	}

	// Top coupled pairs
	seen := make(map[string]bool)
	for a, partners := range coChange {
		for b, co := range partners {
			union := fileOccurrence[a] + fileOccurrence[b] - co
			if union == 0 {
				continue
			}
			score := float64(co) / float64(union)
			if score < summaryCouplingCutoff {
				continue
			}
			key := a + "|" + b
			if seen[key] {
				continue
			}
			seen[key] = true
			summary.MostCoupled = append(summary.MostCoupled, coupledPair{
				FileA: a, FileB: b,
				Score: math.Round(score*100) / 100,
			})
		}
	}
	sort.Slice(summary.MostCoupled, func(i, j int) bool {
		return summary.MostCoupled[i].Score > summary.MostCoupled[j].Score
	})
	if len(summary.MostCoupled) > 10 {
		summary.MostCoupled = summary.MostCoupled[:10]
	}
	if summary.MostCoupled == nil {
		summary.MostCoupled = []coupledPair{}
	}

	writeJSON(w, http.StatusOK, archResponse{
		Files:             files,
		Edges:             edges,
		Summary:           summary,
		CouplingAvailable: couplingAvailable,
	})
}

// ── Math helpers ────────────────────────────────────────────────────────

// adaptiveWeights returns risk-score weights proportional to each metric's
// standard deviation (high-variance metrics dominate). Falls back to equal
// 0.25 weights when every metric is constant. Returned weights always sum to 1.
func adaptiveWeights(fanIns, bugDens, chgFreqs, couplings []float64) (wFanIn, wBug, wChg, wCoup float64) {
	sdFanIn := stddev(fanIns)
	sdBug := stddev(bugDens)
	sdChg := stddev(chgFreqs)
	sdCoup := stddev(couplings)
	totalSD := sdFanIn + sdBug + sdChg + sdCoup

	// Weights from standard deviation; if all zero, equal weights
	if totalSD > 0 {
		return sdFanIn / totalSD, sdBug / totalSD, sdChg / totalSD, sdCoup / totalSD
	}
	return 0.25, 0.25, 0.25, 0.25
}

func stddev(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	var variance float64
	for _, v := range vals {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(vals))
	return math.Sqrt(variance)
}

func maxVal(vals []float64) float64 {
	m := 0.0
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}

func safeNorm(val, max float64) float64 {
	if max == 0 {
		return 0
	}
	return val / max
}

func percentileValue(vals []float64, pct int) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := int(float64(pct) / 100.0 * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func computePercentiles(files []archFile, fanIns, bugDens, chgFreqs, couplings []float64) {
	pctRank := func(vals []float64, val float64) int {
		count := 0
		for _, v := range vals {
			if v < val {
				count++
			}
		}
		return int(math.Round(float64(count) / float64(len(vals)) * 100))
	}
	for i := range files {
		files[i].Percentiles.FanIn = pctRank(fanIns, fanIns[i])
		files[i].Percentiles.BugDensity = pctRank(bugDens, bugDens[i])
		files[i].Percentiles.ChangeFrequency = pctRank(chgFreqs, chgFreqs[i])
		files[i].Percentiles.Coupling = pctRank(couplings, couplings[i])
	}
}

func splitEdgeKey(k string) []string {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return []string{k[:i], k[i+1:]}
		}
	}
	return nil
}

// ── Concurrent query helpers (sqlc-backed) ──────────────────────────────

type archFileInfo struct {
	language string
	symbols  []string
	linesSum int
}

type archEdgeAgg struct {
	kinds map[string]bool
	count int
}

type archPRFiles struct {
	files []string
}

func (s *Server) fetchArchNodes(ctx context.Context, repoID int64, fileMap map[string]*archFileInfo, mu *sync.Mutex) error {
	rows, err := s.store.Q.ListArchNodes(ctx, repoID)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	for _, row := range rows {
		fi, ok := fileMap[row.FilePath]
		if !ok {
			fi = &archFileInfo{language: row.Language}
			fileMap[row.FilePath] = fi
		}
		fi.symbols = append(fi.symbols, row.Name)
		if row.LineSpan > 0 {
			fi.linesSum += int(row.LineSpan)
		}
	}
	return nil
}

func (s *Server) fetchArchEdges(ctx context.Context, repoID int64, fanIn, fanOut map[string]int, fileEdges map[string]*archEdgeAgg, edgeKey func(string, string) string, mu *sync.Mutex) error {
	rows, err := s.store.Q.ListArchFileEdges(ctx, repoID)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	for _, row := range rows {
		fanOut[row.SourcePath]++
		fanIn[row.TargetPath]++
		k := edgeKey(row.SourcePath, row.TargetPath)
		ea, ok := fileEdges[k]
		if !ok {
			ea = &archEdgeAgg{kinds: make(map[string]bool)}
			fileEdges[k] = ea
		}
		ea.kinds[row.Kind] = true
		ea.count++
	}
	return nil
}

func (s *Server) fetchArchBugs(ctx context.Context, repoID int64, bugCount, changeFreq map[string]int, mu *sync.Mutex) error {
	rows, err := s.store.Q.ListArchBugDensity(ctx, repoID)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	for _, row := range rows {
		bugCount[row.FilePath] = int(row.Bugs)
		changeFreq[row.FilePath] = int(row.Prs)
	}
	return nil
}

func (s *Server) fetchArchCoupling(ctx context.Context, repoID int64) ([]archPRFiles, error) {
	rows, err := s.store.Q.ListArchCoupling(ctx, repoID)
	if err != nil {
		return nil, err
	}
	result := make([]archPRFiles, 0, len(rows))
	for _, row := range rows {
		result = append(result, archPRFiles{files: row.Files})
	}
	return result, nil
}
