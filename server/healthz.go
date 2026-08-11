package server

import (
	"encoding/json"
	"net/http"
	"slices"
)

type healthResponse struct {
	Topics map[string]healthTopic `json:"topics"`
}

type healthTopic struct {
	Partitions []healthPartition `json:"partitions,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type healthPartition struct {
	Partition  int32 `json:"partition"`
	Checkpoint int64 `json:"checkpoint"`
	LogEnd     int64 `json:"log_end"`
	Lag        int64 `json:"lag"`
}

// handleHealthz serves GET /healthz: per-topic partition checkpoints, log
// end offsets and lag. Always status 200; a topic whose offsets or
// checkpoints cannot be read degrades to an error entry instead of
// failing the whole response.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	topics := make(map[string]healthTopic, len(s.backends))
	for topic, b := range s.backends {
		topics[topic] = s.buildHealthTopic(r, b)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(healthResponse{Topics: topics}); err != nil {
		s.logger.Error("encode healthz response", "error", err)
	}
}

func (s *Server) buildHealthTopic(r *http.Request, b Backend) healthTopic {
	ends, err := b.offsets.LogEndOffsets(r.Context())
	if err != nil {
		return healthTopic{Error: err.Error()}
	}

	partitions := make([]int32, 0, len(ends))
	for p := range ends {
		partitions = append(partitions, p)
	}
	slices.Sort(partitions)

	result := make([]healthPartition, 0, len(partitions))
	for _, p := range partitions {
		logEnd := ends[p]
		checkpoint, err := b.index.Checkpoint(p)
		if err != nil {
			return healthTopic{Error: err.Error()}
		}

		lag := logEnd - checkpoint - 1
		if lag < 0 {
			lag = 0
		}

		result = append(result, healthPartition{
			Partition:  p,
			Checkpoint: checkpoint,
			LogEnd:     logEnd,
			Lag:        lag,
		})
	}

	return healthTopic{Partitions: result}
}
