// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"bytes"
	b64 "encoding/base64"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	l4g "github.com/alecthomas/log4go"
	"github.com/disintegration/imaging"
	"github.com/golang/freetype"
	"github.com/mattermost/platform/einterfaces"
	"github.com/mattermost/platform/model"
	"github.com/mattermost/platform/store"
	"github.com/mattermost/platform/utils"
)

const (
	TOKEN_TYPE_PASSWORD_RECOVERY  = "password_recovery"
	TOKEN_TYPE_VERIFY_EMAIL       = "verify_email"
	PASSWORD_RECOVER_EXPIRY_TIME  = 1000 * 60 * 60 // 1 hour
	VERIFY_EMAIL_EXPIRY_TIME      = 1000 * 60 * 60 // 1 hour
	IMAGE_PROFILE_PIXEL_DIMENSION = 128
)

func CreateUserWithHash(user *model.User, hash string, data string) (*model.User, *model.AppError) {
	if err := IsUserSignUpAllowed(); err != nil {
		return nil, err
	}

	props := model.MapFromJson(strings.NewReader(data))

	if hash != utils.HashSha256(fmt.Sprintf("%v:%v", data, utils.Cfg.EmailSettings.InviteSalt)) {
		return nil, model.NewAppError("CreateUserWithHash", "api.user.create_user.signup_link_invalid.app_error", nil, "", http.StatusInternalServerError)
	}

	if t, err := strconv.ParseInt(props["time"], 10, 64); err != nil || model.GetMillis()-t > 1000*60*60*48 { // 48 hours
		return nil, model.NewAppError("CreateUserWithHash", "api.user.create_user.signup_link_expired.app_error", nil, "", http.StatusInternalServerError)
	}

	teamId := props["id"]

	var team *model.Team
	if result := <-Srv.Store.Team().Get(teamId); result.Err != nil {
		return nil, result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	user.Email = props["email"]
	user.EmailVerified = true

	var ruser *model.User
	var err *model.AppError
	if ruser, err = CreateUser(user); err != nil {
		return nil, err
	}

	if err := JoinUserToTeam(team, ruser, ""); err != nil {
		return nil, err
	}

	AddDirectChannels(team.Id, ruser)

	return ruser, nil
}

func CreateUserWithInviteId(user *model.User, inviteId string) (*model.User, *model.AppError) {
	if err := IsUserSignUpAllowed(); err != nil {
		return nil, err
	}

	var team *model.Team
	if result := <-Srv.Store.Team().GetByInviteId(inviteId); result.Err != nil {
		return nil, result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	user.EmailVerified = false

	var ruser *model.User
	var err *model.AppError
	if ruser, err = CreateUser(user); err != nil {
		return nil, err
	}

	if err := JoinUserToTeam(team, ruser, ""); err != nil {
		return nil, err
	}

	AddDirectChannels(team.Id, ruser)

	if err := SendWelcomeEmail(ruser.Id, ruser.Email, ruser.EmailVerified, ruser.Locale, utils.GetSiteURL()); err != nil {
		l4g.Error(err.Error())
	}

	return ruser, nil
}

func CreateUserAsAdmin(user *model.User) (*model.User, *model.AppError) {
	ruser, err := CreateUser(user)
	if err != nil {
		return nil, err
	}

	if err := SendWelcomeEmail(ruser.Id, ruser.Email, ruser.EmailVerified, ruser.Locale, utils.GetSiteURL()); err != nil {
		l4g.Error(err.Error())
	}

	return ruser, nil
}

func CreateUserFromSignup(user *model.User) (*model.User, *model.AppError) {
	if err := IsUserSignUpAllowed(); err != nil {
		return nil, err
	}

	if !IsFirstUserAccount() && !*utils.Cfg.TeamSettings.EnableOpenServer {
		err := model.NewAppError("CreateUserFromSignup", "api.user.create_user.no_open_server", nil, "email="+user.Email, http.StatusForbidden)
		return nil, err
	}

	user.EmailVerified = false

	ruser, err := CreateUser(user)
	if err != nil {
		return nil, err
	}

	if err := SendWelcomeEmail(ruser.Id, ruser.Email, ruser.EmailVerified, ruser.Locale, utils.GetSiteURL()); err != nil {
		l4g.Error(err.Error())
	}

	return ruser, nil
}

func IsUserSignUpAllowed() *model.AppError {
	if !utils.Cfg.EmailSettings.EnableSignUpWithEmail || !utils.Cfg.TeamSettings.EnableUserCreation {
		err := model.NewAppError("IsUserSignUpAllowed", "api.user.create_user.signup_email_disabled.app_error", nil, "", http.StatusNotImplemented)
		return err
	}
	return nil
}

func IsFirstUserAccount() bool {
	if SessionCacheLength() == 0 {
		if cr := <-Srv.Store.User().GetTotalUsersCount(); cr.Err != nil {
			l4g.Error(cr.Err)
			return false
		} else {
			count := cr.Data.(int64)
			if count <= 0 {
				return true
			}
		}
	}

	return false
}

func CreateUser(user *model.User) (*model.User, *model.AppError) {
	if !user.IsLDAPUser() && !user.IsSAMLUser() && !CheckUserDomain(user, utils.Cfg.TeamSettings.RestrictCreationToDomains) {
		return nil, model.NewAppError("CreateUser", "api.user.create_user.accepted_domain.app_error", nil, "", http.StatusBadRequest)
	}

	user.Roles = model.ROLE_SYSTEM_USER.Id

	// Below is a special case where the first user in the entire
	// system is granted the system_admin role
	if result := <-Srv.Store.User().GetTotalUsersCount(); result.Err != nil {
		return nil, result.Err
	} else {
		count := result.Data.(int64)
		if count <= 0 {
			user.Roles = model.ROLE_SYSTEM_ADMIN.Id + " " + model.ROLE_SYSTEM_USER.Id
		}
	}

	if _, ok := utils.GetSupportedLocales()[user.Locale]; !ok {
		user.Locale = *utils.Cfg.LocalizationSettings.DefaultClientLocale
	}

	if ruser, err := createUser(user); err != nil {
		return nil, err
	} else {
		// This message goes to everyone, so the teamId, channelId and userId are irrelevant
		message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_NEW_USER, "", "", "", nil)
		message.Add("user_id", ruser.Id)
		go Publish(message)

		return ruser, nil
	}
}

func createUser(user *model.User) (*model.User, *model.AppError) {
	user.MakeNonNil()

	if err := utils.IsPasswordValid(user.Password); user.AuthService == "" && err != nil {
		return nil, err
	}

	if result := <-Srv.Store.User().Save(user); result.Err != nil {
		l4g.Error(utils.T("api.user.create_user.save.error"), result.Err)
		return nil, result.Err
	} else {
		ruser := result.Data.(*model.User)

		if user.EmailVerified {
			if err := VerifyUserEmail(ruser.Id); err != nil {
				l4g.Error(utils.T("api.user.create_user.verified.error"), err)
			}
		}

		pref := model.Preference{UserId: ruser.Id, Category: model.PREFERENCE_CATEGORY_TUTORIAL_STEPS, Name: ruser.Id, Value: "0"}
		if presult := <-Srv.Store.Preference().Save(&model.Preferences{pref}); presult.Err != nil {
			l4g.Error(utils.T("api.user.create_user.tutorial.error"), presult.Err.Message)
		}

		ruser.Sanitize(map[string]bool{})

		return ruser, nil
	}
}

func CreateOAuthUser(service string, userData io.Reader, teamId string) (*model.User, *model.AppError) {
	if !utils.Cfg.TeamSettings.EnableUserCreation {
		return nil, model.NewAppError("CreateOAuthUser", "api.user.create_user.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	var user *model.User
	provider := einterfaces.GetOauthProvider(service)
	if provider == nil {
		return nil, model.NewAppError("CreateOAuthUser", "api.user.create_oauth_user.not_available.app_error", map[string]interface{}{"Service": strings.Title(service)}, "", http.StatusNotImplemented)
	} else {
		user = provider.GetUserFromJson(userData)
	}

	if user == nil {
		return nil, model.NewAppError("CreateOAuthUser", "api.user.create_oauth_user.create.app_error", map[string]interface{}{"Service": service}, "", http.StatusInternalServerError)
	}

	suchan := Srv.Store.User().GetByAuth(user.AuthData, service)
	euchan := Srv.Store.User().GetByEmail(user.Email)

	found := true
	count := 0
	for found {
		if found = IsUsernameTaken(user.Username); found {
			user.Username = user.Username + strconv.Itoa(count)
			count += 1
		}
	}

	if result := <-suchan; result.Err == nil {
		return result.Data.(*model.User), nil
	}

	if result := <-euchan; result.Err == nil {
		authService := result.Data.(*model.User).AuthService
		if authService == "" {
			return nil, model.NewAppError("CreateOAuthUser", "api.user.create_oauth_user.already_attached.app_error", map[string]interface{}{"Service": service, "Auth": model.USER_AUTH_SERVICE_EMAIL}, "email="+user.Email, http.StatusBadRequest)
		} else {
			return nil, model.NewAppError("CreateOAuthUser", "api.user.create_oauth_user.already_attached.app_error", map[string]interface{}{"Service": service, "Auth": authService}, "email="+user.Email, http.StatusBadRequest)
		}
	}

	user.EmailVerified = true

	ruser, err := CreateUser(user)
	if err != nil {
		return nil, err
	}

	if len(teamId) > 0 {
		err = AddUserToTeamByTeamId(teamId, user)
		if err != nil {
			return nil, err
		}

		err = AddDirectChannels(teamId, user)
		if err != nil {
			l4g.Error(err.Error())
		}
	}

	return ruser, nil
}

// Check that a user's email domain matches a list of space-delimited domains as a string.
func CheckUserDomain(user *model.User, domains string) bool {
	if len(domains) == 0 {
		return true
	}

	domainArray := strings.Fields(strings.TrimSpace(strings.ToLower(strings.Replace(strings.Replace(domains, "@", " ", -1), ",", " ", -1))))

	for _, d := range domainArray {
		if strings.HasSuffix(strings.ToLower(user.Email), "@"+d) {
			return true
		}
	}

	return false
}

// Check if the username is already used by another user. Return false if the username is invalid.
func IsUsernameTaken(name string) bool {

	if !model.IsValidUsername(name) {
		return false
	}

	if result := <-Srv.Store.User().GetByUsername(name); result.Err != nil {
		return false
	}

	return true
}

func GetUser(userId string) (*model.User, *model.AppError) {
	if result := <-Srv.Store.User().Get(userId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.User), nil
	}
}

func GetUserByUsername(username string) (*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetByUsername(username); result.Err != nil && result.Err.Id == "store.sql_user.get_by_username.app_error" {
		result.Err.StatusCode = http.StatusNotFound
		return nil, result.Err
	} else {
		return result.Data.(*model.User), nil
	}
}

func GetUserByEmail(email string) (*model.User, *model.AppError) {

	if result := <-Srv.Store.User().GetByEmail(email); result.Err != nil && result.Err.Id == "store.sql_user.missing_account.const" {
		result.Err.StatusCode = http.StatusNotFound
		return nil, result.Err
	} else if result.Err != nil {
		result.Err.StatusCode = http.StatusBadRequest
		return nil, result.Err
	} else {
		return result.Data.(*model.User), nil
	}
}

func GetUserByAuth(authData *string, authService string) (*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetByAuth(authData, authService); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.User), nil
	}
}

func GetUserForLogin(loginId string, onlyLdap bool) (*model.User, *model.AppError) {
	ldapAvailable := *utils.Cfg.LdapSettings.Enable && einterfaces.GetLdapInterface() != nil && utils.IsLicensed() && *utils.License().Features.LDAP

	if result := <-Srv.Store.User().GetForLogin(
		loginId,
		*utils.Cfg.EmailSettings.EnableSignInWithUsername && !onlyLdap,
		*utils.Cfg.EmailSettings.EnableSignInWithEmail && !onlyLdap,
		ldapAvailable,
	); result.Err != nil && result.Err.Id == "store.sql_user.get_for_login.multiple_users" {
		// don't fall back to LDAP in this case since we already know there's an LDAP user, but that it shouldn't work
		result.Err.StatusCode = http.StatusBadRequest
		return nil, result.Err
	} else if result.Err != nil {
		if !ldapAvailable {
			// failed to find user and no LDAP server to fall back on
			result.Err.StatusCode = http.StatusBadRequest
			return nil, result.Err
		}

		// fall back to LDAP server to see if we can find a user
		if ldapUser, ldapErr := einterfaces.GetLdapInterface().GetUser(loginId); ldapErr != nil {
			ldapErr.StatusCode = http.StatusBadRequest
			return nil, ldapErr
		} else {
			return ldapUser, nil
		}
	} else {
		return result.Data.(*model.User), nil
	}
}

func GetUsers(offset int, limit int) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetAllProfiles(offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.User), nil
	}
}

func GetUsersMap(offset int, limit int, asAdmin bool) (map[string]*model.User, *model.AppError) {
	users, err := GetUsers(offset, limit)
	if err != nil {
		return nil, err
	}

	userMap := make(map[string]*model.User, len(users))

	for _, user := range users {
		SanitizeProfile(user, asAdmin)
		userMap[user.Id] = user
	}

	return userMap, nil
}

func GetUsersPage(page int, perPage int, asAdmin bool) ([]*model.User, *model.AppError) {
	users, err := GetUsers(page*perPage, perPage)
	if err != nil {
		return nil, err
	}

	return sanitizeProfiles(users, asAdmin), nil
}

func GetUsersEtag() string {
	return (<-Srv.Store.User().GetEtagForAllProfiles()).Data.(string)
}

func GetUsersInTeam(teamId string, offset int, limit int) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfiles(teamId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.User), nil
	}
}

func GetUsersNotInTeam(teamId string, offset int, limit int) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfilesNotInTeam(teamId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.User), nil
	}
}

func GetUsersInTeamMap(teamId string, offset int, limit int, asAdmin bool) (map[string]*model.User, *model.AppError) {
	users, err := GetUsersInTeam(teamId, offset, limit)
	if err != nil {
		return nil, err
	}

	userMap := make(map[string]*model.User, len(users))

	for _, user := range users {
		SanitizeProfile(user, asAdmin)
		userMap[user.Id] = user
	}

	return userMap, nil
}

func GetUsersInTeamPage(teamId string, page int, perPage int, asAdmin bool) ([]*model.User, *model.AppError) {
	users, err := GetUsersInTeam(teamId, page*perPage, perPage)
	if err != nil {
		return nil, err
	}

	return sanitizeProfiles(users, asAdmin), nil
}

func GetUsersNotInTeamPage(teamId string, page int, perPage int, asAdmin bool) ([]*model.User, *model.AppError) {
	users, err := GetUsersNotInTeam(teamId, page*perPage, perPage)
	if err != nil {
		return nil, err
	}

	return sanitizeProfiles(users, asAdmin), nil
}

func GetUsersInTeamEtag(teamId string) string {
	return (<-Srv.Store.User().GetEtagForProfiles(teamId)).Data.(string)
}

func GetUsersNotInTeamEtag(teamId string) string {
	return (<-Srv.Store.User().GetEtagForProfilesNotInTeam(teamId)).Data.(string)
}

func GetUsersInChannel(channelId string, offset int, limit int) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfilesInChannel(channelId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.User), nil
	}
}

func GetUsersInChannelMap(channelId string, offset int, limit int, asAdmin bool) (map[string]*model.User, *model.AppError) {
	users, err := GetUsersInChannel(channelId, offset, limit)
	if err != nil {
		return nil, err
	}

	userMap := make(map[string]*model.User, len(users))

	for _, user := range users {
		SanitizeProfile(user, asAdmin)
		userMap[user.Id] = user
	}

	return userMap, nil
}

func GetUsersInChannelPage(channelId string, page int, perPage int, asAdmin bool) ([]*model.User, *model.AppError) {
	users, err := GetUsersInChannel(channelId, page*perPage, perPage)
	if err != nil {
		return nil, err
	}

	return sanitizeProfiles(users, asAdmin), nil
}

func GetUsersNotInChannel(teamId string, channelId string, offset int, limit int) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfilesNotInChannel(teamId, channelId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.User), nil
	}
}

func GetUsersNotInChannelMap(teamId string, channelId string, offset int, limit int, asAdmin bool) (map[string]*model.User, *model.AppError) {
	users, err := GetUsersNotInChannel(teamId, channelId, offset, limit)
	if err != nil {
		return nil, err
	}

	userMap := make(map[string]*model.User, len(users))

	for _, user := range users {
		SanitizeProfile(user, asAdmin)
		userMap[user.Id] = user
	}

	return userMap, nil
}

func GetUsersNotInChannelPage(teamId string, channelId string, page int, perPage int, asAdmin bool) ([]*model.User, *model.AppError) {
	users, err := GetUsersNotInChannel(teamId, channelId, page*perPage, perPage)
	if err != nil {
		return nil, err
	}

	return sanitizeProfiles(users, asAdmin), nil
}

func GetUsersWithoutTeamPage(page int, perPage int, asAdmin bool) ([]*model.User, *model.AppError) {
	users, err := GetUsersWithoutTeam(page*perPage, perPage)
	if err != nil {
		return nil, err
	}

	return sanitizeProfiles(users, asAdmin), nil
}

func GetUsersWithoutTeam(offset int, limit int) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfilesWithoutTeam(offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.User), nil
	}
}

func GetUsersByIds(userIds []string, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfileByIds(userIds, true); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)
		return sanitizeProfiles(users, asAdmin), nil
	}
}

func GetUsersByUsernames(usernames []string, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().GetProfilesByUsernames(usernames, ""); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)
		return sanitizeProfiles(users, asAdmin), nil
	}
}

func sanitizeProfiles(users []*model.User, asAdmin bool) []*model.User {
	for _, u := range users {
		SanitizeProfile(u, asAdmin)
	}

	return users
}

func GenerateMfaSecret(userId string) (*model.MfaSecret, *model.AppError) {
	mfaInterface := einterfaces.GetMfaInterface()
	if mfaInterface == nil {
		return nil, model.NewAppError("generateMfaSecret", "api.user.generate_mfa_qr.not_available.app_error", nil, "", http.StatusNotImplemented)
	}

	var user *model.User
	var err *model.AppError
	if user, err = GetUser(userId); err != nil {
		return nil, err
	}

	secret, img, err := mfaInterface.GenerateSecret(user)
	if err != nil {
		return nil, err
	}

	mfaSecret := &model.MfaSecret{Secret: secret, QRCode: b64.StdEncoding.EncodeToString(img)}
	return mfaSecret, nil
}

func ActivateMfa(userId, token string) *model.AppError {
	mfaInterface := einterfaces.GetMfaInterface()
	if mfaInterface == nil {
		err := model.NewAppError("ActivateMfa", "api.user.update_mfa.not_available.app_error", nil, "", http.StatusNotImplemented)
		return err
	}

	var user *model.User
	if result := <-Srv.Store.User().Get(userId); result.Err != nil {
		return result.Err
	} else {
		user = result.Data.(*model.User)
	}

	if len(user.AuthService) > 0 && user.AuthService != model.USER_AUTH_SERVICE_LDAP {
		return model.NewAppError("ActivateMfa", "api.user.activate_mfa.email_and_ldap_only.app_error", nil, "", http.StatusBadRequest)
	}

	if err := mfaInterface.Activate(user, token); err != nil {
		return err
	}

	return nil
}

func DeactivateMfa(userId string) *model.AppError {
	mfaInterface := einterfaces.GetMfaInterface()
	if mfaInterface == nil {
		err := model.NewAppError("DeactivateMfa", "api.user.update_mfa.not_available.app_error", nil, "", http.StatusNotImplemented)
		return err
	}

	if err := mfaInterface.Deactivate(userId); err != nil {
		return err
	}

	return nil
}

func CreateProfileImage(username string, userId string) ([]byte, *model.AppError) {
	colors := []color.NRGBA{
		{197, 8, 126, 255},
		{227, 207, 18, 255},
		{28, 181, 105, 255},
		{35, 188, 224, 255},
		{116, 49, 196, 255},
		{197, 8, 126, 255},
		{197, 19, 19, 255},
		{250, 134, 6, 255},
		{227, 207, 18, 255},
		{123, 201, 71, 255},
		{28, 181, 105, 255},
		{35, 188, 224, 255},
		{116, 49, 196, 255},
		{197, 8, 126, 255},
		{197, 19, 19, 255},
		{250, 134, 6, 255},
		{227, 207, 18, 255},
		{123, 201, 71, 255},
		{28, 181, 105, 255},
		{35, 188, 224, 255},
		{116, 49, 196, 255},
		{197, 8, 126, 255},
		{197, 19, 19, 255},
		{250, 134, 6, 255},
		{227, 207, 18, 255},
		{123, 201, 71, 255},
	}

	h := fnv.New32a()
	h.Write([]byte(userId))
	seed := h.Sum32()

	initial := string(strings.ToUpper(username)[0])

	fontDir, _ := utils.FindDir("fonts")
	fontBytes, err := ioutil.ReadFile(fontDir + utils.Cfg.FileSettings.InitialFont)
	if err != nil {
		return nil, model.NewAppError("CreateProfileImage", "api.user.create_profile_image.default_font.app_error", nil, err.Error(), http.StatusInternalServerError)
	}
	font, err := freetype.ParseFont(fontBytes)
	if err != nil {
		return nil, model.NewAppError("CreateProfileImage", "api.user.create_profile_image.default_font.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	color := colors[int64(seed)%int64(len(colors))]
	dstImg := image.NewRGBA(image.Rect(0, 0, IMAGE_PROFILE_PIXEL_DIMENSION, IMAGE_PROFILE_PIXEL_DIMENSION))
	srcImg := image.White
	draw.Draw(dstImg, dstImg.Bounds(), &image.Uniform{color}, image.ZP, draw.Src)
	size := float64(IMAGE_PROFILE_PIXEL_DIMENSION / 2)

	c := freetype.NewContext()
	c.SetFont(font)
	c.SetFontSize(size)
	c.SetClip(dstImg.Bounds())
	c.SetDst(dstImg)
	c.SetSrc(srcImg)

	pt := freetype.Pt(IMAGE_PROFILE_PIXEL_DIMENSION/6, IMAGE_PROFILE_PIXEL_DIMENSION*2/3)
	_, err = c.DrawString(initial, pt)
	if err != nil {
		return nil, model.NewAppError("CreateProfileImage", "api.user.create_profile_image.initial.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	buf := new(bytes.Buffer)

	if imgErr := png.Encode(buf, dstImg); imgErr != nil {
		return nil, model.NewAppError("CreateProfileImage", "api.user.create_profile_image.encode.app_error", nil, imgErr.Error(), http.StatusInternalServerError)
	} else {
		return buf.Bytes(), nil
	}
}

func GetProfileImage(user *model.User) ([]byte, bool, *model.AppError) {
	var img []byte
	readFailed := false

	if len(*utils.Cfg.FileSettings.DriverName) == 0 {
		var err *model.AppError
		if img, err = CreateProfileImage(user.Username, user.Id); err != nil {
			return nil, false, err
		}
	} else {
		path := "users/" + user.Id + "/profile.png"

		if data, err := utils.ReadFile(path); err != nil {
			readFailed = true

			if img, err = CreateProfileImage(user.Username, user.Id); err != nil {
				return nil, false, err
			}

			if user.LastPictureUpdate == 0 {
				if err := utils.WriteFile(img, path); err != nil {
					return nil, false, err
				}
			}

		} else {
			img = data
		}
	}

	return img, readFailed, nil
}

func SetProfileImage(userId string, imageData *multipart.FileHeader) *model.AppError {
	file, err := imageData.Open()
	defer file.Close()
	if err != nil {
		return model.NewAppError("SetProfileImage", "api.user.upload_profile_user.open.app_error", nil, err.Error(), http.StatusBadRequest)
	}

	// Decode image config first to check dimensions before loading the whole thing into memory later on
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return model.NewAppError("SetProfileImage", "api.user.upload_profile_user.decode_config.app_error", nil, err.Error(), http.StatusBadRequest)
	} else if config.Width*config.Height > model.MaxImageSize {
		return model.NewAppError("SetProfileImage", "api.user.upload_profile_user.too_large.app_error", nil, err.Error(), http.StatusBadRequest)
	}

	file.Seek(0, 0)

	// Decode image into Image object
	img, _, err := image.Decode(file)
	if err != nil {
		return model.NewAppError("SetProfileImage", "api.user.upload_profile_user.decode.app_error", nil, err.Error(), http.StatusBadRequest)
	}

	file.Seek(0, 0)

	orientation, _ := getImageOrientation(file)
	img = makeImageUpright(img, orientation)

	// Scale profile image
	profileWidthAndHeight := 128
	img = imaging.Fill(img, profileWidthAndHeight, profileWidthAndHeight, imaging.Center, imaging.Lanczos)

	buf := new(bytes.Buffer)
	err = png.Encode(buf, img)
	if err != nil {
		return model.NewAppError("SetProfileImage", "api.user.upload_profile_user.encode.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	path := "users/" + userId + "/profile.png"

	if err := utils.WriteFile(buf.Bytes(), path); err != nil {
		return model.NewAppError("SetProfileImage", "api.user.upload_profile_user.upload_profile.app_error", nil, "", http.StatusInternalServerError)
	}

	<-Srv.Store.User().UpdateLastPictureUpdate(userId)

	InvalidateCacheForUser(userId)

	if user, err := GetUser(userId); err != nil {
		l4g.Error(utils.T("api.user.get_me.getting.error"), userId)
	} else {
		options := utils.Cfg.GetSanitizeOptions()
		user.SanitizeProfile(options)

		omitUsers := make(map[string]bool, 1)
		omitUsers[userId] = true
		message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_USER_UPDATED, "", "", "", omitUsers)
		message.Add("user", user)

		Publish(message)
	}

	return nil
}

func UpdatePasswordAsUser(userId, currentPassword, newPassword string) *model.AppError {
	var user *model.User
	var err *model.AppError

	if user, err = GetUser(userId); err != nil {
		return err
	}

	if user == nil {
		err = model.NewAppError("updatePassword", "api.user.update_password.valid_account.app_error", nil, "", http.StatusBadRequest)
		return err
	}

	if user.AuthData != nil && *user.AuthData != "" {
		err = model.NewAppError("updatePassword", "api.user.update_password.oauth.app_error", nil, "auth_service="+user.AuthService, http.StatusBadRequest)
		return err
	}

	if err := doubleCheckPassword(user, currentPassword); err != nil {
		if err.Id == "api.user.check_user_password.invalid.app_error" {
			err = model.NewAppError("updatePassword", "api.user.update_password.incorrect.app_error", nil, "", http.StatusBadRequest)
		}
		return err
	}

	T := utils.GetUserTranslations(user.Locale)

	if err := UpdatePasswordSendEmail(user, newPassword, T("api.user.update_password.menu")); err != nil {
		return err
	}

	return nil
}

func UpdateActiveNoLdap(userId string, active bool) (*model.User, *model.AppError) {
	var user *model.User
	var err *model.AppError
	if user, err = GetUser(userId); err != nil {
		return nil, err
	}

	if user.IsLDAPUser() {
		err := model.NewAppError("UpdateActive", "api.user.update_active.no_deactivate_ldap.app_error", nil, "userId="+user.Id, http.StatusBadRequest)
		err.StatusCode = http.StatusBadRequest
		return nil, err
	}

	return UpdateActive(user, active)
}

func UpdateActive(user *model.User, active bool) (*model.User, *model.AppError) {
	if active {
		user.DeleteAt = 0
	} else {
		user.DeleteAt = model.GetMillis()
	}

	if result := <-Srv.Store.User().Update(user, true); result.Err != nil {
		return nil, result.Err
	} else {
		if user.DeleteAt > 0 {
			if err := RevokeAllSessions(user.Id); err != nil {
				return nil, err
			}
		}

		if extra := <-Srv.Store.Channel().ExtraUpdateByUser(user.Id, model.GetMillis()); extra.Err != nil {
			return nil, extra.Err
		}

		ruser := result.Data.([2]*model.User)[0]
		options := utils.Cfg.GetSanitizeOptions()
		options["passwordupdate"] = false
		ruser.Sanitize(options)

		if !active {
			SetStatusOffline(ruser.Id, false)
		}

		teamsForUser, err := GetTeamsForUser(user.Id)
		if err != nil {
			return nil, err
		}

		for _, team := range teamsForUser {
			channelsForUser, err := GetChannelsForUser(team.Id, user.Id)
			if err != nil {
				return nil, err
			}

			for _, channel := range *channelsForUser {
				InvalidateCacheForChannelMembers(channel.Id)
			}
		}

		return ruser, nil
	}
}

func SanitizeProfile(user *model.User, asAdmin bool) {
	options := utils.Cfg.GetSanitizeOptions()
	if asAdmin {
		options["email"] = true
		options["fullname"] = true
		options["authservice"] = true
	}
	user.SanitizeProfile(options)
}

func UpdateUserAsUser(user *model.User, asAdmin bool) (*model.User, *model.AppError) {
	updatedUser, err := UpdateUser(user, true)
	if err != nil {
		return nil, err
	}

	sendUpdatedUserEvent(*updatedUser, asAdmin)

	return updatedUser, nil
}

func PatchUser(userId string, patch *model.UserPatch, asAdmin bool) (*model.User, *model.AppError) {
	user, err := GetUser(userId)
	if err != nil {
		return nil, err
	}

	user.Patch(patch)

	updatedUser, err := UpdateUser(user, true)
	if err != nil {
		return nil, err
	}

	sendUpdatedUserEvent(*updatedUser, asAdmin)

	return updatedUser, nil
}

func sendUpdatedUserEvent(user model.User, asAdmin bool) {
	SanitizeProfile(&user, asAdmin)

	omitUsers := make(map[string]bool, 1)
	omitUsers[user.Id] = true
	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_USER_UPDATED, "", "", "", omitUsers)
	message.Add("user", user)
	go Publish(message)
}

func UpdateUser(user *model.User, sendNotifications bool) (*model.User, *model.AppError) {
	if result := <-Srv.Store.User().Update(user, false); result.Err != nil {
		return nil, result.Err
	} else {
		rusers := result.Data.([2]*model.User)

		if sendNotifications {
			if rusers[0].Email != rusers[1].Email {
				go func() {
					if err := SendEmailChangeEmail(rusers[1].Email, rusers[0].Email, rusers[0].Locale, utils.GetSiteURL()); err != nil {
						l4g.Error(err.Error())
					}
				}()

				if utils.Cfg.EmailSettings.RequireEmailVerification {
					if err := SendEmailVerification(rusers[0]); err != nil {
						l4g.Error(err.Error())
					}
				}
			}

			if rusers[0].Username != rusers[1].Username {
				go func() {
					if err := SendChangeUsernameEmail(rusers[1].Username, rusers[0].Username, rusers[0].Email, rusers[0].Locale, utils.GetSiteURL()); err != nil {
						l4g.Error(err.Error())
					}
				}()
			}
		}

		InvalidateCacheForUser(user.Id)

		return rusers[0], nil
	}
}

func UpdateUserNotifyProps(userId string, props map[string]string) (*model.User, *model.AppError) {
	var user *model.User
	var err *model.AppError
	if user, err = GetUser(userId); err != nil {
		return nil, err
	}

	user.NotifyProps = props

	var ruser *model.User
	if ruser, err = UpdateUser(user, true); err != nil {
		return nil, err
	}

	return ruser, nil
}

func UpdateMfa(activate bool, userId, token string) *model.AppError {
	if activate {
		if err := ActivateMfa(userId, token); err != nil {
			return err
		}
	} else {
		if err := DeactivateMfa(userId); err != nil {
			return err
		}
	}

	go func() {
		var user *model.User
		var err *model.AppError

		if user, err = GetUser(userId); err != nil {
			l4g.Error(err.Error())
			return
		}

		if err := SendMfaChangeEmail(user.Email, activate, user.Locale, utils.GetSiteURL()); err != nil {
			l4g.Error(err.Error())
		}
	}()

	return nil
}

func UpdatePasswordByUserIdSendEmail(userId, newPassword, method string) *model.AppError {
	var user *model.User
	var err *model.AppError
	if user, err = GetUser(userId); err != nil {
		return err
	}

	return UpdatePasswordSendEmail(user, newPassword, method)
}

func UpdatePassword(user *model.User, newPassword string) *model.AppError {
	if err := utils.IsPasswordValid(newPassword); err != nil {
		return err
	}

	hashedPassword := model.HashPassword(newPassword)

	if result := <-Srv.Store.User().UpdatePassword(user.Id, hashedPassword); result.Err != nil {
		return model.NewAppError("UpdatePassword", "api.user.update_password.failed.app_error", nil, result.Err.Error(), http.StatusInternalServerError)
	}

	return nil
}

func UpdatePasswordSendEmail(user *model.User, newPassword, method string) *model.AppError {
	if err := UpdatePassword(user, newPassword); err != nil {
		return err
	}

	go func() {
		if err := SendPasswordChangeEmail(user.Email, method, user.Locale, utils.GetSiteURL()); err != nil {
			l4g.Error(err.Error())
		}
	}()

	return nil
}

func ResetPasswordFromToken(userSuppliedTokenString, newPassword string) *model.AppError {
	var token *model.Token
	var err *model.AppError
	if token, err = GetPasswordRecoveryToken(userSuppliedTokenString); err != nil {
		return err
	} else {
		if model.GetMillis()-token.CreateAt >= PASSWORD_RECOVER_EXPIRY_TIME {
			return model.NewAppError("resetPassword", "api.user.reset_password.link_expired.app_error", nil, "", http.StatusBadRequest)
		}
	}

	var user *model.User
	if user, err = GetUser(token.Extra); err != nil {
		return err
	}

	if user.IsSSOUser() {
		return model.NewAppError("ResetPasswordFromCode", "api.user.reset_password.sso.app_error", nil, "userId="+user.Id, http.StatusBadRequest)
	}

	T := utils.GetUserTranslations(user.Locale)

	if err := UpdatePasswordSendEmail(user, newPassword, T("api.user.reset_password.method")); err != nil {
		return err
	}

	if err := DeleteToken(token); err != nil {
		l4g.Error(err.Error())
	}

	return nil
}

func SendPasswordReset(email string, siteURL string) (bool, *model.AppError) {
	var user *model.User
	var err *model.AppError
	if user, err = GetUserByEmail(email); err != nil {
		return false, nil
	}

	if user.AuthData != nil && len(*user.AuthData) != 0 {
		return false, model.NewAppError("SendPasswordReset", "api.user.send_password_reset.sso.app_error", nil, "userId="+user.Id, http.StatusBadRequest)
	}

	var token *model.Token
	if token, err = CreatePasswordRecoveryToken(user.Id); err != nil {
		return false, err
	}

	if _, err := SendPasswordResetEmail(user.Email, token, user.Locale, siteURL); err != nil {
		return false, model.NewAppError("SendPasswordReset", "api.user.send_password_reset.send.app_error", nil, "err="+err.Message, http.StatusInternalServerError)
	}

	return true, nil
}

func CreatePasswordRecoveryToken(userId string) (*model.Token, *model.AppError) {
	token := model.NewToken(TOKEN_TYPE_PASSWORD_RECOVERY, userId)

	if result := <-Srv.Store.Token().Save(token); result.Err != nil {
		return nil, result.Err
	}

	return token, nil
}

func GetPasswordRecoveryToken(token string) (*model.Token, *model.AppError) {
	if result := <-Srv.Store.Token().GetByToken(token); result.Err != nil {
		return nil, model.NewAppError("GetPasswordRecoveryToken", "api.user.reset_password.invalid_link.app_error", nil, result.Err.Error(), http.StatusBadRequest)
	} else {
		token := result.Data.(*model.Token)
		if token.Type != TOKEN_TYPE_PASSWORD_RECOVERY {
			return nil, model.NewAppError("GetPasswordRecoveryToken", "api.user.reset_password.broken_token.app_error", nil, "", http.StatusBadRequest)
		}
		return token, nil
	}
}

func DeleteToken(token *model.Token) *model.AppError {
	if result := <-Srv.Store.Token().Delete(token.Token); result.Err != nil {
		return result.Err
	}

	return nil
}

func UpdateUserRoles(userId string, newRoles string) (*model.User, *model.AppError) {
	var user *model.User
	var err *model.AppError
	if user, err = GetUser(userId); err != nil {
		err.StatusCode = http.StatusBadRequest
		return nil, err
	}

	user.Roles = newRoles
	uchan := Srv.Store.User().Update(user, true)
	schan := Srv.Store.Session().UpdateRoles(user.Id, newRoles)

	var ruser *model.User
	if result := <-uchan; result.Err != nil {
		return nil, result.Err
	} else {
		ruser = result.Data.([2]*model.User)[0]
	}

	if result := <-schan; result.Err != nil {
		// soft error since the user roles were still updated
		l4g.Error(result.Err)
	}

	ClearSessionCacheForUser(user.Id)

	return ruser, nil
}

func PermanentDeleteUser(user *model.User) *model.AppError {
	l4g.Warn(utils.T("api.user.permanent_delete_user.attempting.warn"), user.Email, user.Id)
	if user.IsInRole(model.ROLE_SYSTEM_ADMIN.Id) {
		l4g.Warn(utils.T("api.user.permanent_delete_user.system_admin.warn"), user.Email)
	}

	if _, err := UpdateActive(user, false); err != nil {
		return err
	}

	if result := <-Srv.Store.Session().PermanentDeleteSessionsByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.UserAccessToken().DeleteAllForUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.OAuth().PermanentDeleteAuthDataByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Webhook().PermanentDeleteIncomingByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Webhook().PermanentDeleteOutgoingByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Command().PermanentDeleteByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Preference().PermanentDeleteByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Channel().PermanentDeleteMembersByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Post().PermanentDeleteByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.User().PermanentDelete(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Audit().PermanentDeleteByUser(user.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Team().RemoveAllMembersByUser(user.Id); result.Err != nil {
		return result.Err
	}

	l4g.Warn(utils.T("api.user.permanent_delete_user.deleted.warn"), user.Email, user.Id)

	return nil
}

func PermanentDeleteAllUsers() *model.AppError {
	if result := <-Srv.Store.User().GetAll(); result.Err != nil {
		return result.Err
	} else {
		users := result.Data.([]*model.User)
		for _, user := range users {
			PermanentDeleteUser(user)
		}
	}

	return nil
}

func SendEmailVerification(user *model.User) *model.AppError {
	token, err := CreateVerifyEmailToken(user.Id)
	if err != nil {
		return err
	}

	if _, err := GetStatus(user.Id); err != nil {
		return SendVerifyEmail(user.Email, user.Locale, utils.GetSiteURL(), token.Token)
	} else {
		return SendEmailChangeVerifyEmail(user.Email, user.Locale, utils.GetSiteURL(), token.Token)
	}
}

func VerifyEmailFromToken(userSuppliedTokenString string) *model.AppError {
	var token *model.Token
	var err *model.AppError
	if token, err = GetVerifyEmailToken(userSuppliedTokenString); err != nil {
		return err
	} else {
		if model.GetMillis()-token.CreateAt >= PASSWORD_RECOVER_EXPIRY_TIME {
			return model.NewAppError("resetPassword", "api.user.reset_password.link_expired.app_error", nil, "", http.StatusBadRequest)
		}
		if err := VerifyUserEmail(token.Extra); err != nil {
			return err
		}
		if err := DeleteToken(token); err != nil {
			l4g.Error(err.Error())
		}
	}

	return nil
}

func CreateVerifyEmailToken(userId string) (*model.Token, *model.AppError) {
	token := model.NewToken(TOKEN_TYPE_VERIFY_EMAIL, userId)

	if result := <-Srv.Store.Token().Save(token); result.Err != nil {
		return nil, result.Err
	}

	return token, nil
}

func GetVerifyEmailToken(token string) (*model.Token, *model.AppError) {
	if result := <-Srv.Store.Token().GetByToken(token); result.Err != nil {
		return nil, model.NewAppError("GetVerifyEmailToken", "api.user.verify_email.bad_link.app_error", nil, result.Err.Error(), http.StatusBadRequest)
	} else {
		token := result.Data.(*model.Token)
		if token.Type != TOKEN_TYPE_VERIFY_EMAIL {
			return nil, model.NewAppError("GetVerifyEmailToken", "api.user.verify_email.broken_token.app_error", nil, "", http.StatusBadRequest)
		}
		return token, nil
	}
}

func VerifyUserEmail(userId string) *model.AppError {
	if err := (<-Srv.Store.User().VerifyEmail(userId)).Err; err != nil {
		return err
	}

	return nil
}

func SearchUsers(props *model.UserSearch, searchOptions map[string]bool, asAdmin bool) ([]*model.User, *model.AppError) {
	if props.WithoutTeam {
		return SearchUsersWithoutTeam(props.Term, searchOptions, asAdmin)
	} else if props.InChannelId != "" {
		return SearchUsersInChannel(props.InChannelId, props.Term, searchOptions, asAdmin)
	} else if props.NotInChannelId != "" {
		return SearchUsersNotInChannel(props.TeamId, props.NotInChannelId, props.Term, searchOptions, asAdmin)
	} else if props.NotInTeamId != "" {
		return SearchUsersNotInTeam(props.NotInTeamId, props.Term, searchOptions, asAdmin)
	} else {
		return SearchUsersInTeam(props.TeamId, props.Term, searchOptions, asAdmin)
	}
}

func SearchUsersInChannel(channelId string, term string, searchOptions map[string]bool, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().SearchInChannel(channelId, term, searchOptions); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		return users, nil
	}
}

func SearchUsersNotInChannel(teamId string, channelId string, term string, searchOptions map[string]bool, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().SearchNotInChannel(teamId, channelId, term, searchOptions); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		return users, nil
	}
}

func SearchUsersInTeam(teamId string, term string, searchOptions map[string]bool, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().Search(teamId, term, searchOptions); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		return users, nil
	}
}

func SearchUsersNotInTeam(notInTeamId string, term string, searchOptions map[string]bool, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().SearchNotInTeam(notInTeamId, term, searchOptions); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		return users, nil
	}
}

func SearchUsersWithoutTeam(term string, searchOptions map[string]bool, asAdmin bool) ([]*model.User, *model.AppError) {
	if result := <-Srv.Store.User().SearchWithoutTeam(term, searchOptions); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		return users, nil
	}
}

func AutocompleteUsersInChannel(teamId string, channelId string, term string, searchOptions map[string]bool, asAdmin bool) (*model.UserAutocompleteInChannel, *model.AppError) {
	uchan := Srv.Store.User().SearchInChannel(channelId, term, searchOptions)
	nuchan := Srv.Store.User().SearchNotInChannel(teamId, channelId, term, searchOptions)

	autocomplete := &model.UserAutocompleteInChannel{}

	if result := <-uchan; result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		autocomplete.InChannel = users
	}

	if result := <-nuchan; result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		autocomplete.OutOfChannel = users
	}

	return autocomplete, nil
}

func AutocompleteUsersInTeam(teamId string, term string, searchOptions map[string]bool, asAdmin bool) (*model.UserAutocompleteInTeam, *model.AppError) {
	autocomplete := &model.UserAutocompleteInTeam{}

	if result := <-Srv.Store.User().Search(teamId, term, searchOptions); result.Err != nil {
		return nil, result.Err
	} else {
		users := result.Data.([]*model.User)

		for _, user := range users {
			SanitizeProfile(user, asAdmin)
		}

		autocomplete.InTeam = users
	}

	return autocomplete, nil
}

func UpdateOAuthUserAttrs(userData io.Reader, user *model.User, provider einterfaces.OauthProvider, service string) *model.AppError {
	oauthUser := provider.GetUserFromJson(userData)

	if oauthUser == nil {
		return model.NewAppError("UpdateOAuthUserAttrs", "api.user.update_oauth_user_attrs.get_user.app_error", map[string]interface{}{"Service": service}, "", http.StatusBadRequest)
	}

	userAttrsChanged := false

	if oauthUser.Username != user.Username {
		if existingUser, _ := GetUserByUsername(oauthUser.Username); existingUser == nil {
			user.Username = oauthUser.Username
			userAttrsChanged = true
		}
	}

	if oauthUser.GetFullName() != user.GetFullName() {
		user.FirstName = oauthUser.FirstName
		user.LastName = oauthUser.LastName
		userAttrsChanged = true
	}

	if oauthUser.Email != user.Email {
		if existingUser, _ := GetUserByEmail(oauthUser.Email); existingUser == nil {
			user.Email = oauthUser.Email
			userAttrsChanged = true
		}
	}

	if userAttrsChanged {
		var result store.StoreResult
		if result = <-Srv.Store.User().Update(user, true); result.Err != nil {
			return result.Err
		}

		user = result.Data.([2]*model.User)[0]
		InvalidateCacheForUser(user.Id)
	}

	return nil
}
