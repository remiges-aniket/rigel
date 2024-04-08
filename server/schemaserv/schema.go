package schemaserv

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/remiges-tech/alya/service"
	"github.com/remiges-tech/alya/wscutils"
	"github.com/remiges-tech/rigel"
	"github.com/remiges-tech/rigel/etcd"
	"github.com/remiges-tech/rigel/server/utils"
	"github.com/remiges-tech/rigel/types"
)

// GetSchemaRequest represents the structure for incoming requests.
type GetSchemaRequest struct {
	App     string `form:"app" validate:"required"`
	Module  string `form:"module" validate:"required"`
	Version int    `form:"ver"  validate:"required"`
}

// getSchemaResponse represents the structure for outgoing  responses.
type GetSchemaResponse struct {
	App         string        `json:"app"`
	Module      string        `json:"module"`
	Ver         int           `json:"ver"`
	Fields      []types.Field `json:"fields"`
	Description string        `json:"description"`
}

// getSchemaResponse represents the structure for outgoing  responses.
type GetSchemaListResponse struct {
	App         string `json:"app"`
	Module      string `json:"module"`
	Ver         int    `json:"ver"`
	Description string `json:"description"`
}

// HandleGetSchemaRequest gets a schema details based on given schemaName , schemaModule and  schemaVersion
func HandleGetSchemaRequest(c *gin.Context, s *service.Service) {
	lh := s.LogHarbour
	lh.Log("GetSchema Request Received")

	// Getting schemaName , schemaModule and schemaVersion  from request parameter
	var getSchemareq GetSchemaRequest

	if err := c.ShouldBindQuery(&getSchemareq); err != nil {
		lh.LogActivity("Error Unmarshalling Query paramaeters to struct:", err.Error())
		invalidJsonError := wscutils.BuildErrorMessage(wscutils.ErrcodeInvalidJson, nil)
		c.JSON(http.StatusBadRequest, wscutils.NewResponse(wscutils.ErrorStatus, nil, []wscutils.ErrorMessage{invalidJsonError}))
		return
	}

	lh.LogActivity("get schema request parsed :", map[string]any{"APP": getSchemareq.App, "Module": getSchemareq.Module, "Version": getSchemareq.Version})

	schemaName := getSchemareq.App
	schemaModule := getSchemareq.Module
	schemaVersion := getSchemareq.Version

	//Validate incoming request
	validationErrors := validateGetSchema(getSchemareq, c)
	if len(validationErrors) > 0 {

		// Log and respond to validation errors
		lh.Debug0().LogDebug("Validation errors:", validationErrors)
		wscutils.SendErrorResponse(c, wscutils.NewResponse(wscutils.ErrorStatus, nil, validationErrors))
		return
	}
	// Extracting Rigel client from service dependency and initializing with values from request parameters.
	rigelClient := s.Dependencies["rigel"]
	client, ok := rigelClient.(*rigel.Rigel)
	if !ok {
		str := "rigelClient"
		lh.Debug0().LogDebug("Invalid Rigel Client Dependency:", validationErrors)
		wscutils.SendErrorResponse(c, wscutils.NewResponse(wscutils.ErrorStatus, nil, []wscutils.ErrorMessage{wscutils.BuildErrorMessage(utils.INVALID_DEPENDENCY, &str)}))
		return
	}
	client.WithApp(schemaName)
	client.WithModule(schemaModule)
	client.WithVersion(schemaVersion)

	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), utils.DIALTIMEOUT)
	defer cancel()

	// Getting schema details
	schema, err := client.GetSchema(ctx)
	if err != nil {
		lh.LogActivity("error occurred while getting Schema details: ", map[string]any{"error": err.Error()})
		wscutils.SendErrorResponse(c, wscutils.NewErrorResponse(SCHEMA_NOT_FOUND))
		return
	}

	// Initialize response struct
	response := GetSchemaResponse{
		App:         schemaName,
		Module:      schemaModule,
		Ver:         schemaVersion,
		Fields:      schema.Fields,
		Description: schema.Description,
	}

	// Send success response
	wscutils.SendSuccessResponse(c, &wscutils.Response{Status: wscutils.SuccessStatus, Data: response, Messages: []wscutils.ErrorMessage{}})

	// Log the completion of execution
	lh.Log("Finished execution of GetSchema")

}

// Validate validates the request body
func validateGetSchema(req GetSchemaRequest, c *gin.Context) []wscutils.ErrorMessage {
	// validate request body using standard validator
	validationErrors := wscutils.WscValidate(req, req.getValsForGetSchemaError)

	// add request-specific vals to validation errors
	if len(validationErrors) > 0 {
		wscutils.SendErrorResponse(c, wscutils.NewResponse(wscutils.ErrorStatus, nil, validationErrors))
	}
	return validationErrors
}

// getValsForGetSchemaError returns a slice of strings to be used as vals for a validation error.
func (req *GetSchemaRequest) getValsForGetSchemaError(err validator.FieldError) []string {
	var vals []string
	switch err.Field() {
	case "App":
		switch err.Tag() {
		case "required":
			vals = append(vals, APP_NAME_REQUIRED)
			vals = append(vals, req.App)
		}
	case "Module":
		switch err.Tag() {
		case "required":
			vals = append(vals, MODULE_NAME_REQUIRED)
			vals = append(vals, req.Module)
		}
	case "Version":
		switch err.Tag() {
		case "required":
			vals = append(vals, VERSION_NAME_REQUIRED)
			vals = append(vals, strconv.Itoa(req.Version))
		}

	}
	return vals
}

// container is used by getSchemaList handler to create and enrich
// schema data it will eventually return. This is required to maintain the state
// of the record while it goes through multiple iterations: first for list of apps,
// then the list of modules inside the app and then versions.
// container holds the state for each iteration.
type container struct {
	appName      string
	moduleName   string
	version      int
	etcd         *etcd.EtcdStorage
	responseData []GetSchemaListResponse
}

func HandleGetSchemaListRequest(c *gin.Context, s *service.Service) {
	lh := s.LogHarbour
	lh.Log("GetSchemaList Request Received")
	// Extracting etcdStorage and rigelTree from service dependency.

	etcd, ok := s.Dependencies["etcd"].(*etcd.EtcdStorage)
	if !ok {
		field := "etcd"
		wscutils.SendErrorResponse(c, wscutils.NewResponse(wscutils.ErrorStatus, nil, []wscutils.ErrorMessage{wscutils.BuildErrorMessage(utils.INVALID_DEPENDENCY, &field)}))
		return
	}

	// Get all keys from etcd
	allkeys, err := etcd.GetWithPrefix(c, "/")
	if err != nil {
		log.Fatalf("etcd interaction failed: %v", err)
		return
	}
	// Build a Rigel STree
	rTree := utils.NewNode("")
	for k, v := range allkeys {
		rTree.AddPath(k, v)
	}

	// r := s.Dependencies["rTree"]
	// rTree, ok := r.(*utils.Node)
	// if !ok {
	// 	field := "rigelTree"
	// 	wscutils.SendErrorResponse(c, wscutils.NewResponse(wscutils.ErrorStatus, nil, []wscutils.ErrorMessage{wscutils.BuildErrorMessage(utils.INVALID_DEPENDENCY, &field)}))
	// 	return
	// }

	container := &container{
		etcd: etcd,
	}

	process(rTree, container)

	// Log the completion of execution
	lh.Log("Finished execution of GetSchemaList")

	wscutils.SendSuccessResponse(c, &wscutils.Response{Status: wscutils.SuccessStatus, Data: map[string]any{"schemas": container.responseData}, Messages: []wscutils.ErrorMessage{}})
}

// process generates the required data by working on rigel keys tree rTree
// it sets the value of the response to be sent in the container at c.responseData
func process(rTree *utils.Node, c *container) {
	appNodes := rTree.Ls(utils.RIGELPREFIX)
	for _, n := range appNodes {
		workOnApps(n, rTree, c)
	}
}

func workOnApps(n *utils.Node, rTree *utils.Node, c *container) {
	appName := n.Name
	moduleNodes := rTree.Ls(utils.RIGELPREFIX + "/" + appName)
	c.appName = appName
	// modules
	for _, m := range moduleNodes {
		workOnModules(m, rTree, c)
	}
}

func workOnModules(m *utils.Node, rTree *utils.Node, c *container) {
	mName := m.Name
	versionNodes := rTree.Ls(utils.RIGELPREFIX + "/" + c.appName + "/" + mName)
	c.moduleName = mName

	for _, v := range versionNodes {
		workOnVersions(v, rTree, c)
	}
}

func workOnVersions(v *utils.Node, rTree *utils.Node, c *container) {
	vName := v.Name
	vInt, err := strconv.Atoi(vName)
	if err != nil {
		wscutils.NewErrorResponse(INVALID_VERSION)
		return
	}

	c.version = vInt

	generateResponse(c)

}

func generateResponse(c *container) {
	descr := getDescr(c)
	response := GetSchemaListResponse{
		App:         c.appName,
		Ver:         c.version,
		Module:      c.moduleName,
		Description: descr,
	}
	c.responseData = append(c.responseData, response)
}

func getDescr(t *container) string {
	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), utils.DIALTIMEOUT)
	defer cancel()

	descr, err := t.etcd.Get(ctx, rigel.GetSchemaDescriptionPath(t.appName, t.moduleName, t.version))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			wscutils.NewErrorResponse(DESCRIPTION_TIMED_OUT)
		} else {
			wscutils.NewErrorResponse(DESCRIPTION_GET_FAILED)
		}
		return ""
	}

	return descr
}
