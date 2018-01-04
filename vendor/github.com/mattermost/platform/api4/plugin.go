// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

// EXPERIMENTAL - SUBJECT TO CHANGE

package api4

import (
	"net/http"

	l4g "github.com/alecthomas/log4go"
	"github.com/mattermost/platform/app"
	"github.com/mattermost/platform/model"
	"github.com/mattermost/platform/utils"
)

const (
	MAXIMUM_PLUGIN_FILE_SIZE = 50 * 1024 * 1024
)

func InitPlugin() {
	l4g.Debug("EXPERIMENTAL: Initializing plugin api")

	BaseRoutes.Plugins.Handle("", ApiSessionRequired(uploadPlugin)).Methods("POST")
	BaseRoutes.Plugins.Handle("", ApiSessionRequired(getPlugins)).Methods("GET")
	BaseRoutes.Plugin.Handle("", ApiSessionRequired(removePlugin)).Methods("DELETE")

}

func uploadPlugin(c *Context, w http.ResponseWriter, r *http.Request) {
	if !*utils.Cfg.PluginSettings.Enable {
		c.Err = model.NewAppError("uploadPlugin", "app.plugin.disabled.app_error", nil, "", http.StatusNotImplemented)
		return
	}

	if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		c.SetPermissionError(model.PERMISSION_MANAGE_SYSTEM)
		return
	}

	if err := r.ParseMultipartForm(MAXIMUM_PLUGIN_FILE_SIZE); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m := r.MultipartForm

	pluginArray, ok := m.File["plugin"]
	if !ok {
		c.Err = model.NewAppError("uploadPlugin", "api.plugin.upload.no_file.app_error", nil, "", http.StatusBadRequest)
		return
	}

	if len(pluginArray) <= 0 {
		c.Err = model.NewAppError("uploadPlugin", "api.plugin.upload.array.app_error", nil, "", http.StatusBadRequest)
		return
	}

	file, err := pluginArray[0].Open()
	if err != nil {
		c.Err = model.NewAppError("uploadPlugin", "api.plugin.upload.file.app_error", nil, "", http.StatusBadRequest)
		return
	}
	defer file.Close()

	manifest, unpackErr := app.UnpackAndActivatePlugin(file)

	if unpackErr != nil {
		c.Err = unpackErr
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(manifest.ToJson()))
}

func getPlugins(c *Context, w http.ResponseWriter, r *http.Request) {
	if !*utils.Cfg.PluginSettings.Enable {
		c.Err = model.NewAppError("getPlugins", "app.plugin.disabled.app_error", nil, "", http.StatusNotImplemented)
		return
	}

	if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		c.SetPermissionError(model.PERMISSION_MANAGE_SYSTEM)
		return
	}

	manifests, err := app.GetActivePluginManifests()
	if err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(model.ManifestListToJson(manifests)))
}

func removePlugin(c *Context, w http.ResponseWriter, r *http.Request) {
	c.RequirePluginId()
	if c.Err != nil {
		return
	}

	if !*utils.Cfg.PluginSettings.Enable {
		c.Err = model.NewAppError("getPlugins", "app.plugin.disabled.app_error", nil, "", http.StatusNotImplemented)
		return
	}

	if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		c.SetPermissionError(model.PERMISSION_MANAGE_SYSTEM)
		return
	}

	err := app.RemovePlugin(c.Params.PluginId)
	if err != nil {
		c.Err = err
		return
	}

	ReturnStatusOK(w)
}
