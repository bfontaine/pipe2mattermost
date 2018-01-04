// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package api

import (
	b64 "encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	l4g "github.com/alecthomas/log4go"
	"github.com/gorilla/mux"
	"github.com/mattermost/platform/app"
	"github.com/mattermost/platform/einterfaces"
	"github.com/mattermost/platform/model"
	"github.com/mattermost/platform/store"
	"github.com/mattermost/platform/utils"
)

func InitUser() {
	l4g.Debug(utils.T("api.user.init.debug"))

	BaseRoutes.Users.Handle("/create", ApiAppHandler(createUser)).Methods("POST")
	BaseRoutes.Users.Handle("/update", ApiUserRequired(updateUser)).Methods("POST")
	BaseRoutes.Users.Handle("/update_active", ApiUserRequired(updateActive)).Methods("POST")
	BaseRoutes.Users.Handle("/update_notify", ApiUserRequired(updateUserNotify)).Methods("POST")
	BaseRoutes.Users.Handle("/newpassword", ApiUserRequired(updatePassword)).Methods("POST")
	BaseRoutes.Users.Handle("/send_password_reset", ApiAppHandler(sendPasswordReset)).Methods("POST")
	BaseRoutes.Users.Handle("/reset_password", ApiAppHandler(resetPassword)).Methods("POST")
	BaseRoutes.Users.Handle("/login", ApiAppHandler(login)).Methods("POST")
	BaseRoutes.Users.Handle("/logout", ApiAppHandler(logout)).Methods("POST")
	BaseRoutes.Users.Handle("/revoke_session", ApiUserRequired(revokeSession)).Methods("POST")
	BaseRoutes.Users.Handle("/attach_device", ApiUserRequired(attachDeviceId)).Methods("POST")
	//DEPRICATED FOR SECURITY USE APIV4 BaseRoutes.Users.Handle("/verify_email", ApiAppHandler(verifyEmail)).Methods("POST")
	//DEPRICATED FOR SECURITY USE APIV4 BaseRoutes.Users.Handle("/resend_verification", ApiAppHandler(resendVerification)).Methods("POST")
	BaseRoutes.Users.Handle("/newimage", ApiUserRequired(uploadProfileImage)).Methods("POST")
	BaseRoutes.Users.Handle("/me", ApiUserRequired(getMe)).Methods("GET")
	BaseRoutes.Users.Handle("/initial_load", ApiAppHandler(getInitialLoad)).Methods("GET")
	BaseRoutes.Users.Handle("/{offset:[0-9]+}/{limit:[0-9]+}", ApiUserRequired(getProfiles)).Methods("GET")
	BaseRoutes.NeedTeam.Handle("/users/{offset:[0-9]+}/{limit:[0-9]+}", ApiUserRequired(getProfilesInTeam)).Methods("GET")
	BaseRoutes.NeedChannel.Handle("/users/{offset:[0-9]+}/{limit:[0-9]+}", ApiUserRequired(getProfilesInChannel)).Methods("GET")
	BaseRoutes.NeedChannel.Handle("/users/not_in_channel/{offset:[0-9]+}/{limit:[0-9]+}", ApiUserRequired(getProfilesNotInChannel)).Methods("GET")
	BaseRoutes.Users.Handle("/search", ApiUserRequired(searchUsers)).Methods("POST")
	BaseRoutes.Users.Handle("/ids", ApiUserRequired(getProfilesByIds)).Methods("POST")
	BaseRoutes.Users.Handle("/autocomplete", ApiUserRequired(autocompleteUsers)).Methods("GET")

	BaseRoutes.NeedTeam.Handle("/users/autocomplete", ApiUserRequired(autocompleteUsersInTeam)).Methods("GET")
	BaseRoutes.NeedChannel.Handle("/users/autocomplete", ApiUserRequired(autocompleteUsersInChannel)).Methods("GET")

	BaseRoutes.Users.Handle("/mfa", ApiAppHandler(checkMfa)).Methods("POST")
	BaseRoutes.Users.Handle("/generate_mfa_secret", ApiUserRequiredMfa(generateMfaSecret)).Methods("GET")
	BaseRoutes.Users.Handle("/update_mfa", ApiUserRequiredMfa(updateMfa)).Methods("POST")

	BaseRoutes.Users.Handle("/claim/email_to_oauth", ApiAppHandler(emailToOAuth)).Methods("POST")
	BaseRoutes.Users.Handle("/claim/oauth_to_email", ApiUserRequired(oauthToEmail)).Methods("POST")
	BaseRoutes.Users.Handle("/claim/email_to_ldap", ApiAppHandler(emailToLdap)).Methods("POST")
	BaseRoutes.Users.Handle("/claim/ldap_to_email", ApiAppHandler(ldapToEmail)).Methods("POST")

	BaseRoutes.NeedUser.Handle("/get", ApiUserRequired(getUser)).Methods("GET")
	BaseRoutes.Users.Handle("/name/{username:[A-Za-z0-9_\\-.]+}", ApiUserRequired(getByUsername)).Methods("GET")
	BaseRoutes.Users.Handle("/email/{email}", ApiUserRequired(getByEmail)).Methods("GET")
	BaseRoutes.NeedUser.Handle("/sessions", ApiUserRequired(getSessions)).Methods("GET")
	BaseRoutes.NeedUser.Handle("/audits", ApiUserRequired(getAudits)).Methods("GET")
	BaseRoutes.NeedUser.Handle("/image", ApiUserRequiredTrustRequester(getProfileImage)).Methods("GET")
	BaseRoutes.NeedUser.Handle("/update_roles", ApiUserRequired(updateRoles)).Methods("POST")

	BaseRoutes.Root.Handle("/login/sso/saml", AppHandlerIndependent(loginWithSaml)).Methods("GET")
	BaseRoutes.Root.Handle("/login/sso/saml", AppHandlerIndependent(completeSaml)).Methods("POST")
}

func createUser(c *Context, w http.ResponseWriter, r *http.Request) {
	user := model.UserFromJson(r.Body)

	if user == nil {
		c.SetInvalidParam("createUser", "user")
		return
	}

	hash := r.URL.Query().Get("h")
	inviteId := r.URL.Query().Get("iid")

	var ruser *model.User
	var err *model.AppError
	if len(hash) > 0 {
		ruser, err = app.CreateUserWithHash(user, hash, r.URL.Query().Get("d"))
	} else if len(inviteId) > 0 {
		ruser, err = app.CreateUserWithInviteId(user, inviteId)
	} else {
		ruser, err = app.CreateUserFromSignup(user)
	}

	if err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(ruser.ToJson()))
}

func login(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	id := props["id"]
	loginId := props["login_id"]
	password := props["password"]
	mfaToken := props["token"]
	deviceId := props["device_id"]
	ldapOnly := props["ldap_only"] == "true"

	c.LogAudit("attempt - user_id=" + id + " login_id=" + loginId)
	user, err := app.AuthenticateUserForLogin(id, loginId, password, mfaToken, deviceId, ldapOnly)
	if err != nil {
		c.LogAudit("failure - user_id=" + id + " login_id=" + loginId)
		c.Err = err
		return
	}

	c.LogAuditWithUserId(user.Id, "success")

	doLogin(c, w, r, user, deviceId)
	if c.Err != nil {
		return
	}

	user.Sanitize(map[string]bool{})

	w.Write([]byte(user.ToJson()))
}

// User MUST be authenticated completely before calling Login
func doLogin(c *Context, w http.ResponseWriter, r *http.Request, user *model.User, deviceId string) {
	session, err := app.DoLogin(w, r, user, deviceId)
	if err != nil {
		c.Err = err
		return
	}

	c.Session = *session
}

func revokeSession(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)
	id := props["id"]

	if err := app.RevokeSessionById(id); err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(model.MapToJson(props)))
}

func attachDeviceId(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	deviceId := props["device_id"]
	if len(deviceId) == 0 {
		c.SetInvalidParam("attachDevice", "deviceId")
		return
	}

	// A special case where we logout of all other sessions with the same device id
	if err := app.RevokeSessionsForDeviceId(c.Session.UserId, deviceId, c.Session.Id); err != nil {
		c.Err = err
		c.Err.StatusCode = http.StatusInternalServerError
		return
	}

	app.ClearSessionCacheForUser(c.Session.UserId)
	c.Session.SetExpireInDays(*utils.Cfg.ServiceSettings.SessionLengthMobileInDays)

	maxAge := *utils.Cfg.ServiceSettings.SessionLengthMobileInDays * 60 * 60 * 24

	secure := false
	if app.GetProtocol(r) == "https" {
		secure = true
	}

	expiresAt := time.Unix(model.GetMillis()/1000+int64(maxAge), 0)
	sessionCookie := &http.Cookie{
		Name:     model.SESSION_COOKIE_TOKEN,
		Value:    c.Session.Token,
		Path:     "/",
		MaxAge:   maxAge,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure,
	}

	http.SetCookie(w, sessionCookie)

	if err := app.AttachDeviceId(c.Session.Id, deviceId, c.Session.ExpiresAt); err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(model.MapToJson(props)))
}

func getSessions(c *Context, w http.ResponseWriter, r *http.Request) {

	params := mux.Vars(r)
	id := params["user_id"]

	if !app.SessionHasPermissionToUser(c.Session, id) {
		c.SetPermissionError(model.PERMISSION_EDIT_OTHER_USERS)
		return
	}

	if sessions, err := app.GetSessions(id); err != nil {
		c.Err = err
		return
	} else {
		for _, session := range sessions {
			session.Sanitize()
		}

		w.Write([]byte(model.SessionsToJson(sessions)))
	}
}

func logout(c *Context, w http.ResponseWriter, r *http.Request) {
	data := make(map[string]string)
	data["user_id"] = c.Session.UserId

	Logout(c, w, r)
	if c.Err == nil {
		w.Write([]byte(model.MapToJson(data)))
	}
}

func Logout(c *Context, w http.ResponseWriter, r *http.Request) {
	c.LogAudit("")
	c.RemoveSessionCookie(w, r)
	if c.Session.Id != "" {
		if err := app.RevokeSessionById(c.Session.Id); err != nil {
			c.Err = err
			return
		}
	}
}

func getMe(c *Context, w http.ResponseWriter, r *http.Request) {

	if user, err := app.GetUser(c.Session.UserId); err != nil {
		c.Err = err
		c.RemoveSessionCookie(w, r)
		l4g.Error(utils.T("api.user.get_me.getting.error"), c.Session.UserId)
		return
	} else if HandleEtag(user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress), "Get Me", w, r) {
		return
	} else {
		user.Sanitize(map[string]bool{})
		w.Header().Set(model.HEADER_ETAG_SERVER, user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress))
		w.Write([]byte(user.ToJson()))
		return
	}
}

func getInitialLoad(c *Context, w http.ResponseWriter, r *http.Request) {

	il := model.InitialLoad{}

	if len(c.Session.UserId) != 0 {
		var err *model.AppError

		il.User, err = app.GetUser(c.Session.UserId)
		if err != nil {
			c.Err = err
			return
		}
		il.User.Sanitize(map[string]bool{})

		il.Preferences, err = app.GetPreferencesForUser(c.Session.UserId)
		if err != nil {
			c.Err = err
			return
		}

		il.Teams, err = app.GetTeamsForUser(c.Session.UserId)
		if err != nil {
			c.Err = err
			return
		}

		for _, team := range il.Teams {
			team.Sanitize()
		}

		il.TeamMembers = c.Session.TeamMembers
	}

	if app.SessionCacheLength() == 0 {
		// Below is a special case when intializating a new server
		// Lets check to make sure the server is really empty

		il.NoAccounts = app.IsFirstUserAccount()
	}

	il.ClientCfg = utils.ClientCfg
	if app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		il.LicenseCfg = utils.ClientLicense()
	} else {
		il.LicenseCfg = utils.GetSanitizedClientLicense()
	}

	w.Write([]byte(il.ToJson()))
}

func getUser(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	id := params["user_id"]

	var user *model.User
	var err *model.AppError

	if user, err = app.GetUser(id); err != nil {
		c.Err = err
		return
	}

	etag := user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress)

	if HandleEtag(etag, "Get User", w, r) {
		return
	} else {
		app.SanitizeProfile(user, c.IsSystemAdmin())
		w.Header().Set(model.HEADER_ETAG_SERVER, etag)
		w.Write([]byte(user.ToJson()))
		return
	}
}

func getByUsername(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	username := params["username"]

	var user *model.User
	var err *model.AppError

	if user, err = app.GetUserByUsername(username); err != nil {
		c.Err = err
		return
	} else if HandleEtag(user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress), "Get By Username", w, r) {
		return
	} else {
		sanitizeProfile(c, user)

		w.Header().Set(model.HEADER_ETAG_SERVER, user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress))
		w.Write([]byte(user.ToJson()))
		return
	}
}

func getByEmail(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	email := params["email"]

	if user, err := app.GetUserByEmail(email); err != nil {
		c.Err = err
		return
	} else if HandleEtag(user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress), "Get By Email", w, r) {
		return
	} else {
		sanitizeProfile(c, user)

		w.Header().Set(model.HEADER_ETAG_SERVER, user.Etag(utils.Cfg.PrivacySettings.ShowFullName, utils.Cfg.PrivacySettings.ShowEmailAddress))
		w.Write([]byte(user.ToJson()))
		return
	}
}

func getProfiles(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)

	offset, err := strconv.Atoi(params["offset"])
	if err != nil {
		c.SetInvalidParam("getProfiles", "offset")
		return
	}

	limit, err := strconv.Atoi(params["limit"])
	if err != nil {
		c.SetInvalidParam("getProfiles", "limit")
		return
	}

	etag := app.GetUsersEtag() + params["offset"] + "." + params["limit"]
	if HandleEtag(etag, "Get Profiles", w, r) {
		return
	}

	if profiles, err := app.GetUsersMap(offset, limit, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		w.Header().Set(model.HEADER_ETAG_SERVER, etag)
		w.Write([]byte(model.UserMapToJson(profiles)))
	}
}

func getProfilesInTeam(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	teamId := params["team_id"]

	if c.Session.GetTeamByTeamId(teamId) == nil {
		if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
			return
		}
	}

	offset, err := strconv.Atoi(params["offset"])
	if err != nil {
		c.SetInvalidParam("getProfilesInTeam", "offset")
		return
	}

	limit, err := strconv.Atoi(params["limit"])
	if err != nil {
		c.SetInvalidParam("getProfilesInTeam", "limit")
		return
	}

	etag := app.GetUsersInTeamEtag(teamId)
	if HandleEtag(etag, "Get Profiles In Team", w, r) {
		return
	}

	if profiles, err := app.GetUsersInTeamMap(teamId, offset, limit, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		w.Header().Set(model.HEADER_ETAG_SERVER, etag)
		w.Write([]byte(model.UserMapToJson(profiles)))
	}
}

func getProfilesInChannel(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	channelId := params["channel_id"]

	offset, err := strconv.Atoi(params["offset"])
	if err != nil {
		c.SetInvalidParam("getProfiles", "offset")
		return
	}

	limit, err := strconv.Atoi(params["limit"])
	if err != nil {
		c.SetInvalidParam("getProfiles", "limit")
		return
	}

	if c.Session.GetTeamByTeamId(c.TeamId) == nil {
		if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
			c.SetPermissionError(model.PERMISSION_MANAGE_SYSTEM)
			return
		}
	}

	if !app.SessionHasPermissionToChannel(c.Session, channelId, model.PERMISSION_READ_CHANNEL) {
		c.SetPermissionError(model.PERMISSION_READ_CHANNEL)
		return
	}

	if profiles, err := app.GetUsersInChannelMap(channelId, offset, limit, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		w.Write([]byte(model.UserMapToJson(profiles)))
	}
}

func getProfilesNotInChannel(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	channelId := params["channel_id"]

	if c.Session.GetTeamByTeamId(c.TeamId) == nil {
		if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
			c.SetPermissionError(model.PERMISSION_MANAGE_SYSTEM)
			return
		}
	}

	if !app.SessionHasPermissionToChannel(c.Session, channelId, model.PERMISSION_READ_CHANNEL) {
		c.SetPermissionError(model.PERMISSION_READ_CHANNEL)
		return
	}

	offset, err := strconv.Atoi(params["offset"])
	if err != nil {
		c.SetInvalidParam("getProfiles", "offset")
		return
	}

	limit, err := strconv.Atoi(params["limit"])
	if err != nil {
		c.SetInvalidParam("getProfiles", "limit")
		return
	}

	if profiles, err := app.GetUsersNotInChannelMap(c.TeamId, channelId, offset, limit, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		w.Write([]byte(model.UserMapToJson(profiles)))
	}
}

func getAudits(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	id := params["user_id"]

	if !app.SessionHasPermissionToUser(c.Session, id) {
		c.SetPermissionError(model.PERMISSION_EDIT_OTHER_USERS)
		return
	}

	if audits, err := app.GetAudits(id, 20); err != nil {
		c.Err = err
		return
	} else {
		etag := audits.Etag()

		if HandleEtag(etag, "Get Audits", w, r) {
			return
		}

		if len(etag) > 0 {
			w.Header().Set(model.HEADER_ETAG_SERVER, etag)
		}

		w.Write([]byte(audits.ToJson()))
		return
	}
}

func getProfileImage(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	id := params["user_id"]
	readFailed := false

	var etag string

	if users, err := app.GetUsersByIds([]string{id}, false); err != nil {
		c.Err = err
		return
	} else {
		if len(users) == 0 {
			c.Err = model.NewAppError("getProfileImage", "store.sql_user.get_profiles.app_error", nil, "", http.StatusInternalServerError)
			return
		}

		user := users[0]
		etag = strconv.FormatInt(user.LastPictureUpdate, 10)
		if HandleEtag(etag, "Profile Image", w, r) {
			return
		}

		var img []byte
		img, readFailed, err = app.GetProfileImage(user)
		if err != nil {
			c.Err = err
			return
		}

		if readFailed {
			w.Header().Set("Cache-Control", "max-age=300, public") // 5 mins
		} else {
			w.Header().Set("Cache-Control", "max-age=86400, public") // 24 hrs
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set(model.HEADER_ETAG_SERVER, etag)
		w.Write(img)
	}
}

func uploadProfileImage(c *Context, w http.ResponseWriter, r *http.Request) {
	if len(*utils.Cfg.FileSettings.DriverName) == 0 {
		c.Err = model.NewAppError("uploadProfileImage", "api.user.upload_profile_user.storage.app_error", nil, "", http.StatusNotImplemented)
		return
	}

	if r.ContentLength > *utils.Cfg.FileSettings.MaxFileSize {
		c.Err = model.NewAppError("uploadProfileImage", "api.user.upload_profile_user.too_large.app_error", nil, "", http.StatusRequestEntityTooLarge)
		return
	}

	if err := r.ParseMultipartForm(*utils.Cfg.FileSettings.MaxFileSize); err != nil {
		c.Err = model.NewAppError("uploadProfileImage", "api.user.upload_profile_user.parse.app_error", nil, "", http.StatusBadRequest)
		return
	}

	m := r.MultipartForm

	imageArray, ok := m.File["image"]
	if !ok {
		c.Err = model.NewAppError("uploadProfileImage", "api.user.upload_profile_user.no_file.app_error", nil, "", http.StatusBadRequest)
		return
	}

	if len(imageArray) <= 0 {
		c.Err = model.NewAppError("uploadProfileImage", "api.user.upload_profile_user.array.app_error", nil, "", http.StatusBadRequest)
		return
	}

	imageData := imageArray[0]

	if err := app.SetProfileImage(c.Session.UserId, imageData); err != nil {
		c.Err = err
		return
	}

	c.LogAudit("")

	// write something as the response since jQuery expects a json response
	w.Write([]byte("true"))
}

func updateUser(c *Context, w http.ResponseWriter, r *http.Request) {
	user := model.UserFromJson(r.Body)

	if user == nil {
		c.SetInvalidParam("updateUser", "user")
		return
	}

	if !app.SessionHasPermissionToUser(c.Session, user.Id) {
		c.SetPermissionError(model.PERMISSION_EDIT_OTHER_USERS)
		return
	}

	if ruser, err := app.UpdateUserAsUser(user, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		c.LogAudit("")
		w.Write([]byte(ruser.ToJson()))
	}
}

func updatePassword(c *Context, w http.ResponseWriter, r *http.Request) {
	c.LogAudit("attempted")

	props := model.MapFromJson(r.Body)
	userId := props["user_id"]
	if len(userId) != 26 {
		c.SetInvalidParam("updatePassword", "user_id")
		return
	}

	currentPassword := props["current_password"]
	if len(currentPassword) <= 0 {
		c.SetInvalidParam("updatePassword", "current_password")
		return
	}

	newPassword := props["new_password"]

	if userId != c.Session.UserId {
		c.Err = model.NewAppError("updatePassword", "api.user.update_password.context.app_error", nil, "", http.StatusForbidden)
		return
	}

	if err := app.UpdatePasswordAsUser(userId, currentPassword, newPassword); err != nil {
		c.LogAudit("failed")
		c.Err = err
		return
	} else {
		c.LogAudit("completed")

		data := make(map[string]string)
		data["user_id"] = c.Session.UserId
		w.Write([]byte(model.MapToJson(data)))
	}
}

func updateRoles(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)
	params := mux.Vars(r)

	userId := params["user_id"]
	if len(userId) != 26 {
		c.SetInvalidParam("updateMemberRoles", "user_id")
		return
	}

	newRoles := props["new_roles"]
	if !(model.IsValidUserRoles(newRoles)) {
		c.SetInvalidParam("updateMemberRoles", "new_roles")
		return
	}

	if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_ROLES) {
		c.SetPermissionError(model.PERMISSION_MANAGE_ROLES)
		return
	}

	if _, err := app.UpdateUserRoles(userId, newRoles); err != nil {
		return
	} else {
		c.LogAuditWithUserId(userId, "roles="+newRoles)
	}

	rdata := map[string]string{}
	rdata["status"] = "ok"
	w.Write([]byte(model.MapToJson(rdata)))
}

func updateActive(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	userId := props["user_id"]
	if len(userId) != 26 {
		c.SetInvalidParam("updateActive", "user_id")
		return
	}

	active := props["active"] == "true"

	// true when you're trying to de-activate yourself
	isSelfDeactive := !active && userId == c.Session.UserId

	if !isSelfDeactive && !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		c.Err = model.NewAppError("updateActive", "api.user.update_active.permissions.app_error", nil, "userId="+userId, http.StatusForbidden)
		return
	}

	if ruser, err := app.UpdateActiveNoLdap(userId, active); err != nil {
		c.Err = err
	} else {
		c.LogAuditWithUserId(ruser.Id, fmt.Sprintf("active=%v", active))
		w.Write([]byte(ruser.ToJson()))
	}
}

func sendPasswordReset(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("sendPasswordReset", "email")
		return
	}

	if sent, err := app.SendPasswordReset(email, utils.GetSiteURL()); err != nil {
		c.Err = err
		return
	} else if sent {
		c.LogAudit("sent=" + email)
	}

	w.Write([]byte(model.MapToJson(props)))
}

func resetPassword(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	code := props["code"]
	if len(code) != model.TOKEN_SIZE {
		c.SetInvalidParam("resetPassword", "code")
		return
	}

	newPassword := props["new_password"]

	c.LogAudit("attempt - token=" + code)

	if err := app.ResetPasswordFromToken(code, newPassword); err != nil {
		c.LogAudit("fail - token=" + code)
		c.Err = err
		return
	}

	c.LogAudit("success - token=" + code)

	rdata := map[string]string{}
	rdata["status"] = "ok"
	w.Write([]byte(model.MapToJson(rdata)))
}

func updateUserNotify(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	userId := props["user_id"]
	if len(userId) != 26 {
		c.SetInvalidParam("updateUserNotify", "user_id")
		return
	}

	if !app.SessionHasPermissionToUser(c.Session, userId) {
		c.SetPermissionError(model.PERMISSION_EDIT_OTHER_USERS)
		return
	}

	delete(props, "user_id")

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("updateUserNotify", "email")
		return
	}

	desktop_sound := props["desktop_sound"]
	if len(desktop_sound) == 0 {
		c.SetInvalidParam("updateUserNotify", "desktop_sound")
		return
	}

	desktop := props["desktop"]
	if len(desktop) == 0 {
		c.SetInvalidParam("updateUserNotify", "desktop")
		return
	}

	comments := props["comments"]
	if len(comments) == 0 {
		c.SetInvalidParam("updateUserNotify", "comments")
		return
	}

	ruser, err := app.UpdateUserNotifyProps(userId, props)
	if err != nil {
		c.Err = err
		return
	}

	c.LogAuditWithUserId(ruser.Id, "")

	options := utils.Cfg.GetSanitizeOptions()
	options["passwordupdate"] = false
	ruser.Sanitize(options)
	w.Write([]byte(ruser.ToJson()))
}

func emailToOAuth(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	password := props["password"]
	if len(password) == 0 {
		c.SetInvalidParam("emailToOAuth", "password")
		return
	}

	mfaToken := props["token"]

	service := props["service"]
	if len(service) == 0 {
		c.SetInvalidParam("emailToOAuth", "service")
		return
	}

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("emailToOAuth", "email")
		return
	}

	link, err := app.SwitchEmailToOAuth(w, r, email, password, mfaToken, service)
	if err != nil {
		c.Err = err
		return
	}

	c.LogAudit("success for email=" + email)
	w.Write([]byte(model.MapToJson(map[string]string{"follow_link": link})))
}

func oauthToEmail(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	password := props["password"]
	if err := utils.IsPasswordValid(password); err != nil {
		c.Err = err
		return
	}

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("oauthToEmail", "email")
		return
	}

	link, err := app.SwitchOAuthToEmail(email, password, c.Session.UserId)
	if err != nil {
		c.Err = err
		return
	}

	c.RemoveSessionCookie(w, r)
	if c.Err != nil {
		return
	}

	c.LogAudit("success")
	w.Write([]byte(model.MapToJson(map[string]string{"follow_link": link})))
}

func emailToLdap(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("emailToLdap", "email")
		return
	}

	emailPassword := props["email_password"]
	if len(emailPassword) == 0 {
		c.SetInvalidParam("emailToLdap", "email_password")
		return
	}

	ldapId := props["ldap_id"]
	if len(ldapId) == 0 {
		c.SetInvalidParam("emailToLdap", "ldap_id")
		return
	}

	ldapPassword := props["ldap_password"]
	if len(ldapPassword) == 0 {
		c.SetInvalidParam("emailToLdap", "ldap_password")
		return
	}

	token := props["token"]

	c.LogAudit("attempt")

	link, err := app.SwitchEmailToLdap(email, emailPassword, token, ldapId, ldapPassword)
	if err != nil {
		c.Err = err
		return
	}

	c.RemoveSessionCookie(w, r)
	if c.Err != nil {
		return
	}

	c.LogAudit("success")
	w.Write([]byte(model.MapToJson(map[string]string{"follow_link": link})))
}

func ldapToEmail(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("ldapToEmail", "email")
		return
	}

	emailPassword := props["email_password"]
	if err := utils.IsPasswordValid(emailPassword); err != nil {
		c.Err = err
		return
	}

	ldapPassword := props["ldap_password"]
	if len(ldapPassword) == 0 {
		c.SetInvalidParam("ldapToEmail", "ldap_password")
		return
	}

	token := props["token"]

	c.LogAudit("attempt")

	link, err := app.SwitchLdapToEmail(ldapPassword, token, email, emailPassword)
	if err != nil {
		c.Err = err
		return
	}

	c.RemoveSessionCookie(w, r)
	if c.Err != nil {
		return
	}

	c.LogAudit("success")
	w.Write([]byte(model.MapToJson(map[string]string{"follow_link": link})))
}

/* Disabling for security reasons. Use apiv4
func verifyEmail(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	userId := props["uid"]
	if len(userId) != 26 {
		c.SetInvalidParam("verifyEmail", "uid")
		return
	}

	hashedId := props["hid"]
	if len(hashedId) == 0 {
		c.SetInvalidParam("verifyEmail", "hid")
		return
	}

	if model.ComparePassword(hashedId, userId+utils.Cfg.EmailSettings.InviteSalt) {
		if c.Err = app.VerifyUserEmail(userId); c.Err != nil {
			return
		} else {
			c.LogAudit("Email Verified")
			return
		}
	}

	c.Err = model.NewAppError("verifyEmail", "api.user.verify_email.bad_link.app_error", nil, "", http.StatusBadRequest)
}

func resendVerification(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.MapFromJson(r.Body)

	email := props["email"]
	if len(email) == 0 {
		c.SetInvalidParam("resendVerification", "email")
		return
	}

	if user, error := app.GetUserForLogin(email, false); error != nil {
		c.Err = error
		return
	} else {
		if _, err := app.GetStatus(user.Id); err != nil {
			go app.SendVerifyEmail(user.Id, user.Email, user.Locale, utils.GetSiteURL())
		} else {
			go app.SendEmailChangeVerifyEmail(user.Id, user.Email, user.Locale, utils.GetSiteURL())
		}
	}
}*/

func generateMfaSecret(c *Context, w http.ResponseWriter, r *http.Request) {
	secret, err := app.GenerateMfaSecret(c.Session.UserId)
	if err != nil {
		c.Err = err
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write([]byte(secret.ToJson()))
}

func updateMfa(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.StringInterfaceFromJson(r.Body)

	activate, ok := props["activate"].(bool)
	if !ok {
		c.SetInvalidParam("updateMfa", "activate")
		return
	}

	token := ""
	if activate {
		token = props["token"].(string)
		if len(token) == 0 {
			c.SetInvalidParam("updateMfa", "token")
			return
		}
	}

	c.LogAudit("attempt")

	if activate {
		if err := app.ActivateMfa(c.Session.UserId, token); err != nil {
			c.Err = err
			return
		}
		c.LogAudit("success - activated")
	} else {
		if err := app.DeactivateMfa(c.Session.UserId); err != nil {
			c.Err = err
			return
		}
		c.LogAudit("success - deactivated")
	}

	go func() {
		var user *model.User
		var err *model.AppError
		if user, err = app.GetUser(c.Session.UserId); err != nil {
			l4g.Warn(err.Error())
			return
		}

		if err := app.SendMfaChangeEmail(user.Email, activate, user.Locale, utils.GetSiteURL()); err != nil {
			l4g.Error(err.Error())
		}
	}()

	rdata := map[string]string{}
	rdata["status"] = "ok"
	w.Write([]byte(model.MapToJson(rdata)))
}

func checkMfa(c *Context, w http.ResponseWriter, r *http.Request) {
	if !utils.IsLicensed() || !*utils.License().Features.MFA || !*utils.Cfg.ServiceSettings.EnableMultifactorAuthentication {
		rdata := map[string]string{}
		rdata["mfa_required"] = "false"
		w.Write([]byte(model.MapToJson(rdata)))
		return
	}

	props := model.MapFromJson(r.Body)

	loginId := props["login_id"]
	if len(loginId) == 0 {
		c.SetInvalidParam("checkMfa", "login_id")
		return
	}

	rdata := map[string]string{}
	if user, err := app.GetUserForLogin(loginId, false); err != nil {
		rdata["mfa_required"] = "false"
	} else {
		rdata["mfa_required"] = strconv.FormatBool(user.MfaActive)
	}
	w.Write([]byte(model.MapToJson(rdata)))
}

func loginWithSaml(c *Context, w http.ResponseWriter, r *http.Request) {
	samlInterface := einterfaces.GetSamlInterface()

	if samlInterface == nil {
		c.Err = model.NewAppError("loginWithSaml", "api.user.saml.not_available.app_error", nil, "", http.StatusFound)
		return
	}

	teamId, err := app.GetTeamIdFromQuery(r.URL.Query())
	if err != nil {
		c.Err = err
		return
	}
	action := r.URL.Query().Get("action")
	redirectTo := r.URL.Query().Get("redirect_to")
	relayProps := map[string]string{}
	relayState := ""

	if len(action) != 0 {
		relayProps["team_id"] = teamId
		relayProps["action"] = action
		if action == model.OAUTH_ACTION_EMAIL_TO_SSO {
			relayProps["email"] = r.URL.Query().Get("email")
		}
	}

	if len(redirectTo) != 0 {
		relayProps["redirect_to"] = redirectTo
	}

	if len(relayProps) > 0 {
		relayState = b64.StdEncoding.EncodeToString([]byte(model.MapToJson(relayProps)))
	}

	if data, err := samlInterface.BuildRequest(relayState); err != nil {
		c.Err = err
		return
	} else {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		http.Redirect(w, r, data.URL, http.StatusFound)
	}
}

func completeSaml(c *Context, w http.ResponseWriter, r *http.Request) {
	samlInterface := einterfaces.GetSamlInterface()

	if samlInterface == nil {
		c.Err = model.NewAppError("completeSaml", "api.user.saml.not_available.app_error", nil, "", http.StatusFound)
		return
	}

	//Validate that the user is with SAML and all that
	encodedXML := r.FormValue("SAMLResponse")
	relayState := r.FormValue("RelayState")

	relayProps := make(map[string]string)
	if len(relayState) > 0 {
		stateStr := ""
		if b, err := b64.StdEncoding.DecodeString(relayState); err != nil {
			c.Err = model.NewAppError("completeSaml", "api.user.authorize_oauth_user.invalid_state.app_error", nil, err.Error(), http.StatusFound)
			return
		} else {
			stateStr = string(b)
		}
		relayProps = model.MapFromJson(strings.NewReader(stateStr))
	}

	action := relayProps["action"]
	if user, err := samlInterface.DoLogin(encodedXML, relayProps); err != nil {
		if action == model.OAUTH_ACTION_MOBILE {
			err.Translate(c.T)
			w.Write([]byte(err.ToJson()))
		} else {
			c.Err = err
			c.Err.StatusCode = http.StatusFound
		}
		return
	} else {
		if err := app.CheckUserAdditionalAuthenticationCriteria(user, ""); err != nil {
			c.Err = err
			c.Err.StatusCode = http.StatusFound
			return
		}

		switch action {
		case model.OAUTH_ACTION_SIGNUP:
			teamId := relayProps["team_id"]
			if len(teamId) > 0 {
				go app.AddDirectChannels(teamId, user)
			}
			break
		case model.OAUTH_ACTION_EMAIL_TO_SSO:
			if err := app.RevokeAllSessions(user.Id); err != nil {
				c.Err = err
				return
			}
			c.LogAuditWithUserId(user.Id, "Revoked all sessions for user")
			go func() {
				if err := app.SendSignInChangeEmail(user.Email, strings.Title(model.USER_AUTH_SERVICE_SAML)+" SSO", user.Locale, utils.GetSiteURL()); err != nil {
					l4g.Error(err.Error())
				}
			}()
			break
		}
		doLogin(c, w, r, user, "")
		if c.Err != nil {
			return
		}

		if val, ok := relayProps["redirect_to"]; ok {
			http.Redirect(w, r, c.GetSiteURLHeader()+val, http.StatusFound)
			return
		}

		if action == model.OAUTH_ACTION_MOBILE {
			ReturnStatusOK(w)
		} else {
			http.Redirect(w, r, app.GetProtocol(r)+"://"+r.Host, http.StatusFound)
		}
	}
}

func sanitizeProfile(c *Context, user *model.User) *model.User {
	options := utils.Cfg.GetSanitizeOptions()

	if app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		options["email"] = true
		options["fullname"] = true
		options["authservice"] = true
	}

	user.SanitizeProfile(options)

	return user
}

func searchUsers(c *Context, w http.ResponseWriter, r *http.Request) {
	props := model.UserSearchFromJson(r.Body)
	if props == nil {
		c.SetInvalidParam("searchUsers", "")
		return
	}

	if len(props.Term) == 0 {
		c.SetInvalidParam("searchUsers", "term")
		return
	}

	if props.InChannelId != "" && !app.SessionHasPermissionToChannel(c.Session, props.InChannelId, model.PERMISSION_READ_CHANNEL) {
		c.SetPermissionError(model.PERMISSION_READ_CHANNEL)
		return
	}

	if props.NotInChannelId != "" && !app.SessionHasPermissionToChannel(c.Session, props.NotInChannelId, model.PERMISSION_READ_CHANNEL) {
		c.SetPermissionError(model.PERMISSION_READ_CHANNEL)
		return
	}

	searchOptions := map[string]bool{}
	searchOptions[store.USER_SEARCH_OPTION_ALLOW_INACTIVE] = props.AllowInactive

	if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		hideFullName := !utils.Cfg.PrivacySettings.ShowFullName
		hideEmail := !utils.Cfg.PrivacySettings.ShowEmailAddress

		if hideFullName && hideEmail {
			searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY_NO_FULL_NAME] = true
		} else if hideFullName {
			searchOptions[store.USER_SEARCH_OPTION_ALL_NO_FULL_NAME] = true
		} else if hideEmail {
			searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY] = true
		}
	}

	if profiles, err := app.SearchUsers(props, searchOptions, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		w.Write([]byte(model.UserListToJson(profiles)))
	}
}

func getProfilesByIds(c *Context, w http.ResponseWriter, r *http.Request) {
	userIds := model.ArrayFromJson(r.Body)

	if len(userIds) == 0 {
		c.SetInvalidParam("getProfilesByIds", "user_ids")
		return
	}

	if profiles, err := app.GetUsersByIds(userIds, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	} else {
		profileMap := map[string]*model.User{}
		for _, p := range profiles {
			profileMap[p.Id] = p
		}
		w.Write([]byte(model.UserMapToJson(profileMap)))
	}
}

func autocompleteUsersInChannel(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	channelId := params["channel_id"]
	teamId := params["team_id"]

	term := r.URL.Query().Get("term")

	if c.Session.GetTeamByTeamId(teamId) == nil {
		if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
			return
		}
	}

	if !app.SessionHasPermissionToChannel(c.Session, channelId, model.PERMISSION_READ_CHANNEL) {
		c.SetPermissionError(model.PERMISSION_READ_CHANNEL)
		return
	}

	searchOptions := map[string]bool{}

	hideFullName := !utils.Cfg.PrivacySettings.ShowFullName
	if hideFullName && !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY_NO_FULL_NAME] = true
	} else {
		searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY] = true
	}

	autocomplete, err := app.AutocompleteUsersInChannel(teamId, channelId, term, searchOptions, c.IsSystemAdmin())
	if err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(autocomplete.ToJson()))
}

func autocompleteUsersInTeam(c *Context, w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	teamId := params["team_id"]

	term := r.URL.Query().Get("term")

	if c.Session.GetTeamByTeamId(teamId) == nil {
		if !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
			return
		}
	}

	searchOptions := map[string]bool{}

	hideFullName := !utils.Cfg.PrivacySettings.ShowFullName
	if hideFullName && !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY_NO_FULL_NAME] = true
	} else {
		searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY] = true
	}

	autocomplete, err := app.AutocompleteUsersInTeam(teamId, term, searchOptions, c.IsSystemAdmin())
	if err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(autocomplete.ToJson()))
}

func autocompleteUsers(c *Context, w http.ResponseWriter, r *http.Request) {
	term := r.URL.Query().Get("term")

	searchOptions := map[string]bool{}

	hideFullName := !utils.Cfg.PrivacySettings.ShowFullName
	if hideFullName && !app.SessionHasPermissionTo(c.Session, model.PERMISSION_MANAGE_SYSTEM) {
		searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY_NO_FULL_NAME] = true
	} else {
		searchOptions[store.USER_SEARCH_OPTION_NAMES_ONLY] = true
	}

	var profiles []*model.User
	var err *model.AppError

	if profiles, err = app.SearchUsersInTeam("", term, searchOptions, c.IsSystemAdmin()); err != nil {
		c.Err = err
		return
	}

	w.Write([]byte(model.UserListToJson(profiles)))
}
