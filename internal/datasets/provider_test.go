package datasets

import (
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestThirdPartySuccessSchemasMatchDataDecoders(t *testing.T) {
	for _, pair := range [][2]any{
		{api.GetDatasetsResponse{}, api.GetThirdPartyDatasetsResponse{}},
		{api.GetDataResponse{}, api.GetThirdPartyDataResponse{}},
		{api.PutDataForDownloadResponse{}, api.PutThirdPartyDataForDownloadResponse{}},
		{api.DeleteRowsInDatasetResponse{}, api.DeleteThirdPartyRowsInDatasetResponse{}},
		{api.DeleteAllBadRowsInDatasetViewResponse{}, api.DeleteAllThirdPartyBadRowsInDatasetViewResponse{}},
		{api.ReprocessBadRowsForDatasetViewResponse{}, api.ReprocessThirdPartyBadRowsForDatasetViewResponse{}},
		{api.ReprocessAllBadRowsForDatasetViewResponse{}, api.ReprocessAllThirdPartyBadRowsForDatasetViewResponse{}},
		{api.MoveRowsToQuarantineViewResponse{}, api.MoveThirdPartyRowsToQuarantineViewResponse{}},
		{api.MoveAllRowsToQuarantineViewResponse{}, api.MoveAllThirdPartyRowsToQuarantineViewResponse{}},
		{api.UpdateKeepInQuarantineViewResponse{}, api.UpdateThirdPartyKeepInQuarantineViewResponse{}},
		{api.GetQuarantineCountByStatusResponse{}, api.GetThirdPartyQuarantineCountByStatusResponse{}},
		{api.SearchAndReplaceResponse{}, api.SearchAndReplaceThirdPartyResponse{}},
		{api.ModifyCleanCellResponse{}, api.ModifyThirdPartyCleanCellResponse{}},
		{api.CheckColumnUniquenessResponse{}, api.CheckThirdPartyColumnUniquenessResponse{}},
		{api.DeleteRowsInDataSourceResponse{}, api.DeleteThirdPartyRowsInDataSourceResponse{}},
		{api.DeleteAllBadRowsForDataSourceViewResponse{}, api.DeleteAllThirdPartyBadRowsForDataSourceViewResponse{}},
		{api.ReprocessAllBadRowsForDataSourceViewResponse{}, api.ReprocessAllThirdPartyBadRowsForDataSourceViewResponse{}},
		{api.UpdateKeepInQuarantineForDataSourceViewResponse{}, api.UpdateThirdPartyKeepInQuarantineForDataSourceViewResponse{}},
		{api.DeleteRowsInStoredItemResponse{}, api.DeleteThirdPartyRowsInStoredItemResponse{}},
	} {
		first, third := reflect.TypeOf(pair[0]), reflect.TypeOf(pair[1])
		t.Run(third.Name(), func(t *testing.T) {
			firstBody, firstOK := first.FieldByName("JSON200")
			thirdBody, thirdOK := third.FieldByName("JSON200")
			if !firstOK || !thirdOK || firstBody.Type != thirdBody.Type {
				t.Fatalf("shared decoder for %s no longer matches %s", first.Name(), third.Name())
			}
		})
	}
}
