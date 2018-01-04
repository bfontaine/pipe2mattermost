// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package api4

import (
	"bytes"
	"io/ioutil"
	"os"
	"testing"

	"github.com/mattermost/platform/app"
	"github.com/mattermost/platform/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlugin(t *testing.T) {
	pluginDir, err := ioutil.TempDir("", "mm-plugin-test")
	require.NoError(t, err)
	defer func() {
		os.RemoveAll(pluginDir)
	}()
	webappDir, err := ioutil.TempDir("", "mm-webapp-test")
	require.NoError(t, err)
	defer func() {
		os.RemoveAll(webappDir)
	}()

	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()

	app.StartupPlugins(pluginDir, webappDir)

	enablePlugins := *utils.Cfg.PluginSettings.Enable
	defer func() {
		*utils.Cfg.PluginSettings.Enable = enablePlugins
	}()
	*utils.Cfg.PluginSettings.Enable = true

	path, _ := utils.FindDir("tests")
	file, err := os.Open(path + "/testplugin.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	// Successful upload
	manifest, resp := th.SystemAdminClient.UploadPlugin(file)
	defer func() {
		os.RemoveAll("plugins/testplugin")
	}()
	CheckNoError(t, resp)

	assert.Equal(t, "testplugin", manifest.Id)

	// Upload error cases
	_, resp = th.SystemAdminClient.UploadPlugin(bytes.NewReader([]byte("badfile")))
	CheckBadRequestStatus(t, resp)

	*utils.Cfg.PluginSettings.Enable = false
	_, resp = th.SystemAdminClient.UploadPlugin(file)
	CheckNotImplementedStatus(t, resp)

	*utils.Cfg.PluginSettings.Enable = true
	_, resp = th.Client.UploadPlugin(file)
	CheckForbiddenStatus(t, resp)

	// Successful get
	manifests, resp := th.SystemAdminClient.GetPlugins()
	CheckNoError(t, resp)

	found := false
	for _, m := range manifests {
		if m.Id == manifest.Id {
			found = true
		}
	}

	assert.True(t, found)

	// Get error cases
	*utils.Cfg.PluginSettings.Enable = false
	_, resp = th.SystemAdminClient.GetPlugins()
	CheckNotImplementedStatus(t, resp)

	*utils.Cfg.PluginSettings.Enable = true
	_, resp = th.Client.GetPlugins()
	CheckForbiddenStatus(t, resp)

	// Successful remove
	ok, resp := th.SystemAdminClient.RemovePlugin(manifest.Id)
	CheckNoError(t, resp)

	assert.True(t, ok)

	// Remove error cases
	ok, resp = th.SystemAdminClient.RemovePlugin(manifest.Id)
	CheckBadRequestStatus(t, resp)

	assert.False(t, ok)

	*utils.Cfg.PluginSettings.Enable = false
	_, resp = th.SystemAdminClient.RemovePlugin(manifest.Id)
	CheckNotImplementedStatus(t, resp)

	*utils.Cfg.PluginSettings.Enable = true
	_, resp = th.Client.RemovePlugin(manifest.Id)
	CheckForbiddenStatus(t, resp)

	_, resp = th.SystemAdminClient.RemovePlugin("bad.id")
	CheckNotFoundStatus(t, resp)

	app.Srv.PluginEnv = nil
}
