// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	l4g "github.com/alecthomas/log4go"

	"github.com/mattermost/platform/model"
	"github.com/mattermost/platform/utils"
)

func CreateTeam(team *model.Team) (*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().Save(team); result.Err != nil {
		return nil, result.Err
	} else {
		rteam := result.Data.(*model.Team)

		if _, err := CreateDefaultChannels(rteam.Id); err != nil {
			return nil, err
		}

		return rteam, nil
	}
}

func CreateTeamWithUser(team *model.Team, userId string) (*model.Team, *model.AppError) {
	var user *model.User
	var err *model.AppError
	if user, err = GetUser(userId); err != nil {
		return nil, err
	} else {
		team.Email = user.Email
	}

	if !isTeamEmailAllowed(user) {
		return nil, model.NewAppError("isTeamEmailAllowed", "api.team.is_team_creation_allowed.domain.app_error", nil, "", http.StatusBadRequest)
	}

	var rteam *model.Team
	if rteam, err = CreateTeam(team); err != nil {
		return nil, err
	}

	if err = JoinUserToTeam(rteam, user, ""); err != nil {
		return nil, err
	}

	return rteam, nil
}

func isTeamEmailAddressAllowed(email string) bool {
	email = strings.ToLower(email)
	// commas and @ signs are optional
	// can be in the form of "@corp.mattermost.com, mattermost.com mattermost.org" -> corp.mattermost.com mattermost.com mattermost.org
	domains := strings.Fields(strings.TrimSpace(strings.ToLower(strings.Replace(strings.Replace(utils.Cfg.TeamSettings.RestrictCreationToDomains, "@", " ", -1), ",", " ", -1))))

	matched := false
	for _, d := range domains {
		if strings.HasSuffix(email, "@"+d) {
			matched = true
			break
		}
	}

	if len(utils.Cfg.TeamSettings.RestrictCreationToDomains) > 0 && !matched {
		return false
	}

	return true
}

func isTeamEmailAllowed(user *model.User) bool {
	email := strings.ToLower(user.Email)

	if len(user.AuthService) > 0 && len(*user.AuthData) > 0 {
		return true
	}

	return isTeamEmailAddressAllowed(email)
}

func UpdateTeam(team *model.Team) (*model.Team, *model.AppError) {
	var oldTeam *model.Team
	var err *model.AppError
	if oldTeam, err = GetTeam(team.Id); err != nil {
		return nil, err
	}

	oldTeam.DisplayName = team.DisplayName
	oldTeam.Description = team.Description
	oldTeam.InviteId = team.InviteId
	oldTeam.AllowOpenInvite = team.AllowOpenInvite
	oldTeam.CompanyName = team.CompanyName
	oldTeam.AllowedDomains = team.AllowedDomains

	if result := <-Srv.Store.Team().Update(oldTeam); result.Err != nil {
		return nil, result.Err
	}

	sendUpdatedTeamEvent(oldTeam)

	return oldTeam, nil
}

func PatchTeam(teamId string, patch *model.TeamPatch) (*model.Team, *model.AppError) {
	team, err := GetTeam(teamId)
	if err != nil {
		return nil, err
	}

	team.Patch(patch)

	updatedTeam, err := UpdateTeam(team)
	if err != nil {
		return nil, err
	}

	sendUpdatedTeamEvent(updatedTeam)

	return updatedTeam, nil
}

func sendUpdatedTeamEvent(team *model.Team) {
	sanitizedTeam := &model.Team{}
	*sanitizedTeam = *team
	sanitizedTeam.Sanitize()

	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_UPDATE_TEAM, "", "", "", nil)
	message.Add("team", sanitizedTeam.ToJson())
	go Publish(message)
}

func UpdateTeamMemberRoles(teamId string, userId string, newRoles string) (*model.TeamMember, *model.AppError) {
	var member *model.TeamMember
	if result := <-Srv.Store.Team().GetTeamsForUser(userId); result.Err != nil {
		return nil, result.Err
	} else {
		members := result.Data.([]*model.TeamMember)
		for _, m := range members {
			if m.TeamId == teamId {
				member = m
			}
		}
	}

	if member == nil {
		err := model.NewAppError("UpdateTeamMemberRoles", "api.team.update_member_roles.not_a_member", nil, "userId="+userId+" teamId="+teamId, http.StatusBadRequest)
		return nil, err
	}

	member.Roles = newRoles

	if result := <-Srv.Store.Team().UpdateMember(member); result.Err != nil {
		return nil, result.Err
	}

	ClearSessionCacheForUser(userId)

	sendUpdatedMemberRoleEvent(userId, member)

	return member, nil
}

func sendUpdatedMemberRoleEvent(userId string, member *model.TeamMember) {
	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_MEMBERROLE_UPDATED, "", "", userId, nil)
	message.Add("member", member.ToJson())

	go Publish(message)
}

func AddUserToTeam(teamId string, userId string, userRequestorId string) (*model.Team, *model.AppError) {
	tchan := Srv.Store.Team().Get(teamId)
	uchan := Srv.Store.User().Get(userId)

	var team *model.Team
	if result := <-tchan; result.Err != nil {
		return nil, result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	var user *model.User
	if result := <-uchan; result.Err != nil {
		return nil, result.Err
	} else {
		user = result.Data.(*model.User)
	}

	if err := JoinUserToTeam(team, user, userRequestorId); err != nil {
		return nil, err
	}

	return team, nil
}

func AddUserToTeamByTeamId(teamId string, user *model.User) *model.AppError {
	if result := <-Srv.Store.Team().Get(teamId); result.Err != nil {
		return result.Err
	} else {
		return JoinUserToTeam(result.Data.(*model.Team), user, "")
	}
}

func AddUserToTeamByHash(userId string, hash string, data string) (*model.Team, *model.AppError) {
	props := model.MapFromJson(strings.NewReader(data))

	if hash != utils.HashSha256(fmt.Sprintf("%v:%v", data, utils.Cfg.EmailSettings.InviteSalt)) {
		return nil, model.NewAppError("JoinUserToTeamByHash", "api.user.create_user.signup_link_invalid.app_error", nil, "", http.StatusBadRequest)
	}

	t, timeErr := strconv.ParseInt(props["time"], 10, 64)
	if timeErr != nil || model.GetMillis()-t > 1000*60*60*48 { // 48 hours
		return nil, model.NewAppError("JoinUserToTeamByHash", "api.user.create_user.signup_link_expired.app_error", nil, "", http.StatusBadRequest)
	}

	tchan := Srv.Store.Team().Get(props["id"])
	uchan := Srv.Store.User().Get(userId)

	var team *model.Team
	if result := <-tchan; result.Err != nil {
		return nil, result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	var user *model.User
	if result := <-uchan; result.Err != nil {
		return nil, result.Err
	} else {
		user = result.Data.(*model.User)
	}

	if err := JoinUserToTeam(team, user, ""); err != nil {
		return nil, err
	}

	return team, nil
}

func AddUserToTeamByInviteId(inviteId string, userId string) (*model.Team, *model.AppError) {
	tchan := Srv.Store.Team().GetByInviteId(inviteId)
	uchan := Srv.Store.User().Get(userId)

	var team *model.Team
	if result := <-tchan; result.Err != nil {
		return nil, result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	var user *model.User
	if result := <-uchan; result.Err != nil {
		return nil, result.Err
	} else {
		user = result.Data.(*model.User)
	}

	if err := JoinUserToTeam(team, user, ""); err != nil {
		return nil, err
	}

	return team, nil
}

func joinUserToTeam(team *model.Team, user *model.User) (bool, *model.AppError) {
	tm := &model.TeamMember{
		TeamId: team.Id,
		UserId: user.Id,
		Roles:  model.ROLE_TEAM_USER.Id,
	}

	if team.Email == user.Email {
		tm.Roles = model.ROLE_TEAM_USER.Id + " " + model.ROLE_TEAM_ADMIN.Id
	}

	if etmr := <-Srv.Store.Team().GetMember(team.Id, user.Id); etmr.Err == nil {
		// Membership alredy exists.  Check if deleted and and update, otherwise do nothing
		rtm := etmr.Data.(*model.TeamMember)

		// Do nothing if already added
		if rtm.DeleteAt == 0 {
			return true, nil
		}

		if tmr := <-Srv.Store.Team().UpdateMember(tm); tmr.Err != nil {
			return false, tmr.Err
		}
	} else {
		// Membership appears to be missing.  Lets try to add.
		if tmr := <-Srv.Store.Team().SaveMember(tm); tmr.Err != nil {
			return false, tmr.Err
		}
	}

	return false, nil
}

func JoinUserToTeam(team *model.Team, user *model.User, userRequestorId string) *model.AppError {
	if alreadyAdded, err := joinUserToTeam(team, user); err != nil {
		return err
	} else if alreadyAdded {
		return nil
	}

	if uua := <-Srv.Store.User().UpdateUpdateAt(user.Id); uua.Err != nil {
		return uua.Err
	}

	channelRole := model.ROLE_CHANNEL_USER.Id

	if team.Email == user.Email {
		channelRole = model.ROLE_CHANNEL_USER.Id + " " + model.ROLE_CHANNEL_ADMIN.Id
	}

	// Soft error if there is an issue joining the default channels
	if err := JoinDefaultChannels(team.Id, user, channelRole, userRequestorId); err != nil {
		l4g.Error(utils.T("api.user.create_user.joining.error"), user.Id, team.Id, err)
	}

	ClearSessionCacheForUser(user.Id)
	InvalidateCacheForUser(user.Id)

	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_ADDED_TO_TEAM, "", "", user.Id, nil)
	message.Add("team_id", team.Id)
	message.Add("user_id", user.Id)
	Publish(message)

	return nil
}

func GetTeam(teamId string) (*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().Get(teamId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.Team), nil
	}
}

func GetTeamByName(name string) (*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetByName(name); result.Err != nil {
		result.Err.StatusCode = http.StatusNotFound
		return nil, result.Err
	} else {
		return result.Data.(*model.Team), nil
	}
}

func GetTeamByInviteId(inviteId string) (*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetByInviteId(inviteId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.Team), nil
	}
}

func GetAllTeams() ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetAll(); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func GetAllTeamsPage(offset int, limit int) ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetAllPage(offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func GetAllOpenTeams() ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetAllTeamListing(); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func SearchAllTeams(term string) ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().SearchAll(term); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func SearchOpenTeams(term string) ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().SearchOpen(term); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func GetAllOpenTeamsPage(offset int, limit int) ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetAllTeamPageListing(offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func GetTeamsForUser(userId string) ([]*model.Team, *model.AppError) {
	if result := <-Srv.Store.Team().GetTeamsByUserId(userId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.Team), nil
	}
}

func GetTeamMember(teamId, userId string) (*model.TeamMember, *model.AppError) {
	if result := <-Srv.Store.Team().GetMember(teamId, userId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.TeamMember), nil
	}
}

func GetTeamMembersForUser(userId string) ([]*model.TeamMember, *model.AppError) {
	if result := <-Srv.Store.Team().GetTeamsForUser(userId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.TeamMember), nil
	}
}

func GetTeamMembers(teamId string, offset int, limit int) ([]*model.TeamMember, *model.AppError) {
	if result := <-Srv.Store.Team().GetMembers(teamId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.TeamMember), nil
	}
}

func GetTeamMembersByIds(teamId string, userIds []string) ([]*model.TeamMember, *model.AppError) {
	if result := <-Srv.Store.Team().GetMembersByIds(teamId, userIds); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.TeamMember), nil
	}
}

func AddTeamMember(teamId, userId string) (*model.TeamMember, *model.AppError) {
	if _, err := AddUserToTeam(teamId, userId, ""); err != nil {
		return nil, err
	}

	var teamMember *model.TeamMember
	var err *model.AppError
	if teamMember, err = GetTeamMember(teamId, userId); err != nil {
		return nil, err
	}

	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_ADDED_TO_TEAM, "", "", userId, nil)
	message.Add("team_id", teamId)
	message.Add("user_id", userId)
	Publish(message)

	return teamMember, nil
}

func AddTeamMembers(teamId string, userIds []string, userRequestorId string) ([]*model.TeamMember, *model.AppError) {
	var members []*model.TeamMember

	for _, userId := range userIds {
		if _, err := AddUserToTeam(teamId, userId, userRequestorId); err != nil {
			return nil, err
		}

		if teamMember, err := GetTeamMember(teamId, userId); err != nil {
			return nil, err
		} else {
			members = append(members, teamMember)
		}

		message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_ADDED_TO_TEAM, "", "", userId, nil)
		message.Add("team_id", teamId)
		message.Add("user_id", userId)
		Publish(message)
	}

	return members, nil
}

func AddTeamMemberByHash(userId, hash, data string) (*model.TeamMember, *model.AppError) {
	var team *model.Team
	var err *model.AppError

	if team, err = AddUserToTeamByHash(userId, hash, data); err != nil {
		return nil, err
	}

	if teamMember, err := GetTeamMember(team.Id, userId); err != nil {
		return nil, err
	} else {
		return teamMember, nil
	}
}

func AddTeamMemberByInviteId(inviteId, userId string) (*model.TeamMember, *model.AppError) {
	var team *model.Team
	var err *model.AppError

	if team, err = AddUserToTeamByInviteId(inviteId, userId); err != nil {
		return nil, err
	}

	if teamMember, err := GetTeamMember(team.Id, userId); err != nil {
		return nil, err
	} else {
		return teamMember, nil
	}
}

func GetTeamUnread(teamId, userId string) (*model.TeamUnread, *model.AppError) {
	result := <-Srv.Store.Team().GetChannelUnreadsForTeam(teamId, userId)
	if result.Err != nil {
		return nil, result.Err
	}

	channelUnreads := result.Data.([]*model.ChannelUnread)
	var teamUnread = &model.TeamUnread{
		MsgCount:     0,
		MentionCount: 0,
		TeamId:       teamId,
	}

	for _, cu := range channelUnreads {
		teamUnread.MentionCount += cu.MentionCount

		if cu.NotifyProps["mark_unread"] != model.CHANNEL_MARK_UNREAD_MENTION {
			teamUnread.MsgCount += cu.MsgCount
		}
	}

	return teamUnread, nil
}

func RemoveUserFromTeam(teamId string, userId string) *model.AppError {
	tchan := Srv.Store.Team().Get(teamId)
	uchan := Srv.Store.User().Get(userId)

	var team *model.Team
	if result := <-tchan; result.Err != nil {
		return result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	var user *model.User
	if result := <-uchan; result.Err != nil {
		return result.Err
	} else {
		user = result.Data.(*model.User)
	}

	if err := LeaveTeam(team, user); err != nil {
		return err
	}

	return nil
}

func LeaveTeam(team *model.Team, user *model.User) *model.AppError {
	var teamMember *model.TeamMember
	var err *model.AppError

	if teamMember, err = GetTeamMember(team.Id, user.Id); err != nil {
		return model.NewAppError("LeaveTeam", "api.team.remove_user_from_team.missing.app_error", nil, err.Error(), http.StatusBadRequest)
	}

	var channelList *model.ChannelList

	if result := <-Srv.Store.Channel().GetChannels(team.Id, user.Id); result.Err != nil {
		if result.Err.Id == "store.sql_channel.get_channels.not_found.app_error" {
			channelList = &model.ChannelList{}
		} else {
			return result.Err
		}

	} else {
		channelList = result.Data.(*model.ChannelList)
	}

	for _, channel := range *channelList {
		if !channel.IsGroupOrDirect() {
			InvalidateCacheForChannelMembers(channel.Id)
			if result := <-Srv.Store.Channel().RemoveMember(channel.Id, user.Id); result.Err != nil {
				return result.Err
			}
		}
	}

	// Send the websocket message before we actually do the remove so the user being removed gets it.
	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_LEAVE_TEAM, team.Id, "", "", nil)
	message.Add("user_id", user.Id)
	message.Add("team_id", team.Id)
	Publish(message)

	teamMember.Roles = ""
	teamMember.DeleteAt = model.GetMillis()

	if result := <-Srv.Store.Team().UpdateMember(teamMember); result.Err != nil {
		return result.Err
	}

	if uua := <-Srv.Store.User().UpdateUpdateAt(user.Id); uua.Err != nil {
		return uua.Err
	}

	// delete the preferences that set the last channel used in the team and other team specific preferences
	if result := <-Srv.Store.Preference().DeleteCategory(user.Id, team.Id); result.Err != nil {
		return result.Err
	}

	ClearSessionCacheForUser(user.Id)
	InvalidateCacheForUser(user.Id)

	return nil
}

func InviteNewUsersToTeam(emailList []string, teamId, senderId string) *model.AppError {
	if len(emailList) == 0 {
		err := model.NewAppError("InviteNewUsersToTeam", "api.team.invite_members.no_one.app_error", nil, "", http.StatusBadRequest)
		return err
	}

	var invalidEmailList []string

	for _, email := range emailList {
		if !isTeamEmailAddressAllowed(email) {
			invalidEmailList = append(invalidEmailList, email)
		}
	}

	if len(invalidEmailList) > 0 {
		s := strings.Join(invalidEmailList, ", ")
		err := model.NewAppError("InviteNewUsersToTeam", "api.team.invite_members.invalid_email.app_error", map[string]interface{}{"Addresses": s}, "", http.StatusBadRequest)
		return err
	}

	tchan := Srv.Store.Team().Get(teamId)
	uchan := Srv.Store.User().Get(senderId)

	var team *model.Team
	if result := <-tchan; result.Err != nil {
		return result.Err
	} else {
		team = result.Data.(*model.Team)
	}

	var user *model.User
	if result := <-uchan; result.Err != nil {
		return result.Err
	} else {
		user = result.Data.(*model.User)
	}

	nameFormat := *utils.Cfg.TeamSettings.TeammateNameDisplay
	SendInviteEmails(team, user.GetDisplayName(nameFormat), emailList, utils.GetSiteURL())

	return nil
}

func FindTeamByName(name string) bool {
	if result := <-Srv.Store.Team().GetByName(name); result.Err != nil {
		return false
	} else {
		return true
	}
}

func GetTeamsUnreadForUser(excludeTeamId string, userId string) ([]*model.TeamUnread, *model.AppError) {
	if result := <-Srv.Store.Team().GetChannelUnreadsForAllTeams(excludeTeamId, userId); result.Err != nil {
		return nil, result.Err
	} else {
		data := result.Data.([]*model.ChannelUnread)
		members := []*model.TeamUnread{}
		membersMap := make(map[string]*model.TeamUnread)

		unreads := func(cu *model.ChannelUnread, tu *model.TeamUnread) *model.TeamUnread {
			tu.MentionCount += cu.MentionCount

			if cu.NotifyProps["mark_unread"] != model.CHANNEL_MARK_UNREAD_MENTION {
				tu.MsgCount += cu.MsgCount
			}

			return tu
		}

		for i := range data {
			id := data[i].TeamId
			if mu, ok := membersMap[id]; ok {
				membersMap[id] = unreads(data[i], mu)
			} else {
				membersMap[id] = unreads(data[i], &model.TeamUnread{
					MsgCount:     0,
					MentionCount: 0,
					TeamId:       id,
				})
			}
		}

		for _, val := range membersMap {
			members = append(members, val)
		}

		return members, nil
	}
}

func PermanentDeleteTeamId(teamId string) *model.AppError {
	team, err := GetTeam(teamId)
	if err != nil {
		return err
	}

	return PermanentDeleteTeam(team)
}

func PermanentDeleteTeam(team *model.Team) *model.AppError {
	team.DeleteAt = model.GetMillis()
	if result := <-Srv.Store.Team().Update(team); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Channel().GetTeamChannels(team.Id); result.Err != nil {
		if result.Err.Id != "store.sql_channel.get_channels.not_found.app_error" {
			return result.Err
		}
	} else {
		channels := result.Data.(*model.ChannelList)
		for _, c := range *channels {
			PermanentDeleteChannel(c)
		}
	}

	if result := <-Srv.Store.Team().RemoveAllMembersByTeam(team.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Command().PermanentDeleteByTeam(team.Id); result.Err != nil {
		return result.Err
	}

	if result := <-Srv.Store.Team().PermanentDelete(team.Id); result.Err != nil {
		return result.Err
	}

	return nil
}

func SoftDeleteTeam(teamId string) *model.AppError {
	team, err := GetTeam(teamId)
	if err != nil {
		return err
	}

	team.DeleteAt = model.GetMillis()
	if result := <-Srv.Store.Team().Update(team); result.Err != nil {
		return result.Err
	}

	return nil
}

func GetTeamStats(teamId string) (*model.TeamStats, *model.AppError) {
	tchan := Srv.Store.Team().GetTotalMemberCount(teamId)
	achan := Srv.Store.Team().GetActiveMemberCount(teamId)

	stats := &model.TeamStats{}
	stats.TeamId = teamId

	if result := <-tchan; result.Err != nil {
		return nil, result.Err
	} else {
		stats.TotalMemberCount = result.Data.(int64)
	}

	if result := <-achan; result.Err != nil {
		return nil, result.Err
	} else {
		stats.ActiveMemberCount = result.Data.(int64)
	}

	return stats, nil
}

func GetTeamIdFromQuery(query url.Values) (string, *model.AppError) {
	hash := query.Get("h")
	inviteId := query.Get("id")

	if len(hash) > 0 {
		data := query.Get("d")
		props := model.MapFromJson(strings.NewReader(data))

		if hash != utils.HashSha256(fmt.Sprintf("%v:%v", data, utils.Cfg.EmailSettings.InviteSalt)) {
			return "", model.NewAppError("GetTeamIdFromQuery", "api.oauth.singup_with_oauth.invalid_link.app_error", nil, "", http.StatusBadRequest)
		}

		t, err := strconv.ParseInt(props["time"], 10, 64)
		if err != nil || model.GetMillis()-t > 1000*60*60*48 { // 48 hours
			return "", model.NewAppError("GetTeamIdFromQuery", "api.oauth.singup_with_oauth.expired_link.app_error", nil, "", http.StatusBadRequest)
		}

		return props["id"], nil
	} else if len(inviteId) > 0 {
		if result := <-Srv.Store.Team().GetByInviteId(inviteId); result.Err != nil {
			// soft fail, so we still create user but don't auto-join team
			l4g.Error("%v", result.Err)
		} else {
			return result.Data.(*model.Team).Id, nil
		}
	}

	return "", nil
}

func SanitizeTeam(session model.Session, team *model.Team) *model.Team {
	if !SessionHasPermissionToTeam(session, team.Id, model.PERMISSION_MANAGE_TEAM) {
		team.Sanitize()
	}

	return team
}

func SanitizeTeams(session model.Session, teams []*model.Team) []*model.Team {
	for _, team := range teams {
		SanitizeTeam(session, team)
	}

	return teams
}
