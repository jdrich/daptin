package server

import (
	"reflect"
	"encoding/json"
	"github.com/artpar/api2go"
	"github.com/daptin/daptin/server/resource"
	"github.com/jmoiron/sqlx"
	"github.com/artpar/go.uuid"
	log "github.com/sirupsen/logrus"
	"os"
	"path/filepath"
	"strings"
)

func CheckSystemSecrets(store *resource.ConfigStore) error {
	jwtSecret, err := store.GetConfigValueFor("jwt.secret", "backend")
	if err != nil {
		u, _ := uuid.NewV4()
		jwtSecret = u.String()
		err = store.SetConfigValueFor("jwt.secret", jwtSecret, "backend")
		resource.CheckErr(err, "Failed to store jwt secret")
	}

	encryptionSecret, err := store.GetConfigValueFor("encryption.secret", "backend")

	if err != nil || len(encryptionSecret) < 10 {
		u, _ := uuid.NewV4()
		newSecret := strings.Replace(u.String(), "-", "", -1)
		err = store.SetConfigValueFor("encryption.secret", newSecret, "backend")
	}
	return err

}


func InArrayIndex(val interface{}, array interface{}) (index int) {
	index = -1

	switch reflect.TypeOf(array).Kind() {
	case reflect.Slice:
		s := reflect.ValueOf(array)

		for i := 0; i < s.Len(); i++ {
			if reflect.DeepEqual(val, s.Index(i).Interface()) == true {
				index = i
				return
			}
		}
	}

	return
}

func AddResourcesToApi2Go(api *api2go.API, tables []resource.TableInfo, db *sqlx.DB, ms *resource.MiddlewareSet, configStore *resource.ConfigStore) map[string]*resource.DbResource {
	cruds = make(map[string]*resource.DbResource)
	for _, table := range tables {
		//log.Infof("Table [%v] Relations: %v", table.TableName)

		if table.TableName == "" {
			log.Errorf("Table name is empty, not adding to JSON API, as it will create conflict: %v", table)
			continue
		}

		//for _, r := range table.Relations {
		//log.Infof("Relation :: %v", r.String())
		//}
		model := api2go.NewApi2GoModel(table.TableName, table.Columns, table.DefaultPermission, table.Relations)

		res := resource.NewDbResource(model, db, ms, cruds, configStore, &table)

		cruds[table.TableName] = res
		api.AddResource(model, res)
	}
	return cruds
}

func GetTablesFromWorld(db *sqlx.DB) ([]resource.TableInfo, error) {

	ts := make([]resource.TableInfo, 0)

	res, err := db.Queryx("select table_name, permission, default_permission, " +
		"world_schema_json, is_top_level, is_hidden, is_state_tracking_enabled, default_order" +
		" from world where table_name not like '%_has_%' and table_name not like '%_audit' and table_name not in ('world', 'world_column', 'action', 'user', 'usergroup')")
	if err != nil {
		log.Infof("Failed to select from world table: %v", err)
		return ts, err
	}
	defer res.Close()

	for res.Next() {
		var table_name string
		var permission int64
		var default_permission int64
		var world_schema_json string
		var default_order *string
		var is_top_level bool
		var is_hidden bool
		var is_state_tracking_enabled bool

		err = res.Scan(&table_name, &permission, &default_permission, &world_schema_json, &is_top_level, &is_hidden, &is_state_tracking_enabled, &default_order)
		if err != nil {
			log.Errorf("Failed to scan json schema from world: %v", err)
			continue
		}

		var t resource.TableInfo

		err = json.Unmarshal([]byte(world_schema_json), &t)

		if err != nil {
			log.Errorf("Failed to unmarshal json schema: %v", err)
			continue
		}

		//for _, col := range t.Columns {
		//	if col.ForeignKeyData.Namespace != col.ForeignKeyData.TableName {
		//		col.ForeignKeyData.Namespace = col.ForeignKeyData.TableName
		//	}
		//	if col.ForeignKeyData.KeyName != col.ForeignKeyData.ColumnName {
		//		col.ForeignKeyData.KeyName = col.ForeignKeyData.ColumnName
		//	}
		//}

		t.TableName = table_name
		t.Permission = permission
		t.DefaultPermission = default_permission
		t.IsHidden = is_hidden
		t.IsTopLevel = is_top_level
		t.IsStateTrackingEnabled = is_state_tracking_enabled
		if default_order != nil {
			t.DefaultOrder = *default_order
		}
		ts = append(ts, t)

	}

	log.Infof("Loaded %d tables from world table", len(ts))

	return ts, nil

}

func BuildMiddlewareSet(cmsConfig *resource.CmsConfig) resource.MiddlewareSet {

	var ms resource.MiddlewareSet

	exchangeMiddleware := resource.NewExchangeMiddleware(cmsConfig, &cruds)

	tablePermissionChecker := &resource.TableAccessPermissionChecker{}
	objectPermissionChecker := &resource.ObjectAccessPermissionChecker{}
	dataValidationMiddleware := resource.NewDataValidationMiddleware(cmsConfig, &cruds)

	findOneHandler := resource.NewFindOneEventHandler()
	createEventHandler := resource.NewCreateEventHandler()
	updateEventHandler := resource.NewUpdateEventHandler()
	deleteEventHandler := resource.NewDeleteEventHandler()

	ms.BeforeFindAll = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
	}

	ms.AfterFindAll = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
	}

	ms.BeforeCreate = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		dataValidationMiddleware,
		createEventHandler,
	}
	ms.AfterCreate = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		createEventHandler,
		exchangeMiddleware,
	}

	ms.BeforeDelete = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		deleteEventHandler,
	}
	ms.AfterDelete = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		deleteEventHandler,
	}

	ms.BeforeUpdate = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		dataValidationMiddleware,
		updateEventHandler,
	}
	ms.AfterUpdate = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		updateEventHandler,
	}

	ms.BeforeFindOne = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		findOneHandler,
	}
	ms.AfterFindOne = []resource.DatabaseRequestInterceptor{
		tablePermissionChecker,
		objectPermissionChecker,
		findOneHandler,
	}
	return ms
}

func CleanUpConfigFiles() {

	files, _ := filepath.Glob("schema_*_daptin.*")
	log.Infof("Clean up config files: %v", files)

	for _, fileName := range files {
		os.Remove(fileName)
	}

}
