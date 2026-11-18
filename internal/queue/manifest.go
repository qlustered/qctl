package queue

import (
	"fmt"
	"strconv"
	"time"

	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// MessageSpec is what the producer put on the queue: where the message belongs
// and what it says.
type MessageSpec struct {
	ID                int                    `yaml:"id" json:"id"`
	QName             string                 `yaml:"q_name" json:"q_name"`
	Key               *string                `yaml:"key,omitempty" json:"key,omitempty"`
	DatasetID         *int                   `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	DataSourceModelID *int                   `yaml:"data_source_model_id,omitempty" json:"data_source_model_id,omitempty"`
	Data              map[string]interface{} `yaml:"data,omitempty" json:"data,omitempty"`
}

// MessageStatus is what the queue has done with it since.
//
// MaxReceiveCount is carried next to ReceiveCount rather than left to the
// reader's memory: on its own "receive_count: 4" says nothing about whether the
// message has any attempts left, and the pair is what IsDead is computed from.
type MessageStatus struct {
	Acknowledged    bool    `yaml:"acknowledged" json:"acknowledged"`
	IsDead          bool    `yaml:"is_dead" json:"is_dead"`
	PutCount        int     `yaml:"put_count" json:"put_count"`
	ReceiveCount    int     `yaml:"receive_count" json:"receive_count"`
	MaxReceiveCount *int    `yaml:"max_receive_count,omitempty" json:"max_receive_count,omitempty"`
	RescheduleCount int     `yaml:"reschedule_count" json:"reschedule_count"`
	EnqueuedAt      string  `yaml:"enqueued_at" json:"enqueued_at"`
	DequeuedAt      *string `yaml:"dequeued_at,omitempty" json:"dequeued_at,omitempty"`
	ExpectedAt      *string `yaml:"expected_at,omitempty" json:"expected_at,omitempty"`
	ScheduleAt      *string `yaml:"schedule_at,omitempty" json:"schedule_at,omitempty"`
}

// MessageManifest is the declarative view of one queue message.
//
// There is no apply route for it — the queue is not configuration — so this is
// a describe-only shape. It keeps the manifest form anyway because that is what
// every other `qctl describe` emits, and a reader piping one into jq should not
// have to learn a second layout for this resource.
type MessageManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       MessageSpec          `yaml:"spec" json:"spec"`
	Status     *MessageStatus       `yaml:"status,omitempty" json:"status,omitempty"`
}

// ToManifest converts an API message into the manifest shape describe emits.
func ToManifest(msg *Message) *MessageManifest {
	// The key is the closest thing a queue message has to a human name; it is
	// nullable, and the queue name plus the id is the fallback.
	name := fmt.Sprintf("%s-%d", msg.QName, msg.ID)
	if msg.Key != nil && *msg.Key != "" {
		name = *msg.Key
	}

	labels := map[string]string{"q_name": msg.QName}
	if msg.DatasetID != nil {
		labels["dataset_id"] = strconv.Itoa(*msg.DatasetID)
	}
	if msg.DataSourceModelID != nil {
		labels["data_source_model_id"] = strconv.Itoa(*msg.DataSourceModelID)
	}

	return &MessageManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "QueueMessage",
		Metadata: pkgmanifest.Metadata{
			Name:        name,
			Labels:      labels,
			Annotations: map[string]string{"queue_message_id": strconv.Itoa(msg.ID)},
		},
		Spec: MessageSpec{
			ID:                msg.ID,
			QName:             msg.QName,
			Key:               msg.Key,
			DatasetID:         msg.DatasetID,
			DataSourceModelID: msg.DataSourceModelID,
			Data:              msg.Data,
		},
		Status: &MessageStatus{
			Acknowledged:    msg.Acknowledged,
			IsDead:          IsDead(msg),
			PutCount:        msg.PutCount,
			ReceiveCount:    msg.ReceiveCount,
			MaxReceiveCount: msg.MaxReceiveCount,
			RescheduleCount: msg.RescheduleCount,
			EnqueuedAt:      formatTime(msg.EnqueuedAt),
			DequeuedAt:      formatTimePtr(msg.DequeuedAt),
			ExpectedAt:      formatTimePtr(msg.ExpectedAt),
			ScheduleAt:      formatTimePtr(msg.ScheduleAt),
		},
	}
}

// IsDead reports whether a message has used up its delivery attempts.
//
// The server computes is_dead and sends it, so this reads that field and only
// falls back to recomputing it when the field is absent. The recomputation is
// the server's own rule — unacknowledged AND receive_count >= max_receive_count
// — and it yields false when max_receive_count is absent too, exactly as the
// backend's computed_field does, rather than guessing a maximum.
//
// This is the LISTING definition of dead, which is also what `resurrect` acts
// on. It is NOT the narrower one behind the /dead route and `resurrect-dead`,
// which additionally requires the message to have been idle since its last
// delivery attempt.
func IsDead(msg *Message) bool {
	if msg.IsDead != nil {
		return *msg.IsDead
	}
	if msg.Acknowledged || msg.MaxReceiveCount == nil {
		return false
	}
	return msg.ReceiveCount >= *msg.MaxReceiveCount
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}
