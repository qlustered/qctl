package stored_items

import (
	"reflect"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
)

// A trashed file's trash reaches status.trash, the user named by name; the
// system's trash names nobody; an untrashed file has no trash.
func TestManifestCarriesTheTrash(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	trash := &api.FileTrashSchema{
		Comment: "sent twice", TrashedAt: at,
		TrashedBy:        &api.UserInfoTinyDictSchema{FirstName: "Carol", LastName: "Smith", Email: "carol@example.com"},
		CleanRowsDeleted: 10, BadRowsDeleted: 2, SharedRowsUnlinked: 3, ApprovedRowsCount: 1,
		HeldRowsCount: 4, EditedRowsCount: 5, PersonDeletedRowsCount: 6, RowDeleteIds: []int{7, 8},
	}
	got := APIResponseToManifest(&StoredItemFull{ID: 1, IsTrashed: true, Trash: trash}).Status.Trash
	carol := "Carol Smith"
	want := &FileTrash{
		TrashedAt: "2026-11-16T09:30:00Z", TrashedBy: &carol, Comment: "sent twice",
		CleanRowsDeleted: 10, BadRowsDeleted: 2, SharedRowsUnlinked: 3, ApprovedRowsCount: 1,
		HeldRowsCount: 4, EditedRowsCount: 5, PersonDeletedRowsCount: 6, RowDeleteIDs: []int{7, 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("trash = %+v, want %+v", got, want)
	}

	trash.TrashedBy = &api.UserInfoTinyDictSchema{Email: "nameless@example.com"}
	if got := NewFileTrash(trash).TrashedBy; got == nil || *got != "nameless@example.com" {
		t.Errorf("trashed_by without a name = %v, want the email", got)
	}
	trash.TrashedBy = nil
	if got := NewFileTrash(trash).TrashedBy; got != nil {
		t.Errorf("trashed_by for the system = %q, want nil", *got)
	}
	if got := APIResponseToManifest(&StoredItemFull{ID: 1}).Status.Trash; got != nil {
		t.Errorf("untrashed file has trash %+v", got)
	}
}
