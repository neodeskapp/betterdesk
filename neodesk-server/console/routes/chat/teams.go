package chat

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// permTeamsManage sets up the teams of Team chat: their names, people and
// user groups.
const permTeamsManage = "chat.teams_manage"

// The routes of the teams of Team chat. Every member reads and writes in
// a team; only chat.teams_manage creates, changes and deletes them, and
// sees all of them here, the ones it is no member of too.

// wireUserGroup is a user group as the team dialog shows it.
type wireUserGroup struct {
	GUID    string  `json:"guid"`
	Name    string  `json:"name"`
	Members []int64 `json:"members"`
}

// wireTeam is a team as the managers' list shows it: who is picked by
// hand and which user groups it includes.
type wireTeam struct {
	wireConversation
	People []int64  `json:"people"`
	Groups []string `json:"groups"`
}

// teams answers GET /api/chat/team/teams: every team, the user groups a
// team can include and the people it can name.
func (h *inboxRoutes) teams(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := h.store().List(ctx, chatstore.Filter{Kinds: []string{chatstore.ConvTeam}, Limit: 2000})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]wireTeam, 0, len(list))
	for _, c := range list {
		people, err := h.store().TeamPeople(ctx, c.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		groups, err := h.store().TeamGroups(ctx, c.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		t := wireTeam{wireConversation: conversationOut(c, ""), People: people, Groups: []string{}}
		for _, g := range groups {
			t.Groups = append(t.Groups, g.GUID)
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	groups, members, err := h.store().UserGroups(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	userGroups := make([]wireUserGroup, 0, len(groups))
	for _, g := range groups {
		userGroups = append(userGroups, wireUserGroup{GUID: g.GUID, Name: g.Name, Members: members[g.GUID]})
	}
	people, err := h.people(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	colleagues := []wirePerson{}
	for _, p := range people {
		if h.mayTeam(ctx, p) {
			colleagues = append(colleagues, p)
		}
	}
	sort.Slice(colleagues, func(i, j int) bool { return strings.ToLower(colleagues[i].Name) < strings.ToLower(colleagues[j].Name) })
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "teams": out, "user_groups": userGroups, "people": colleagues})
}

// teamBody is what creating or changing a team sends. A list left out
// stays as it is on a change.
type teamBody struct {
	Title  string   `json:"title"`
	People []int64  `json:"people"`
	Groups []string `json:"groups"`
}

// checkTeam answers 400 and false when the body names more than a team
// takes, someone without Team chat or a user group that doesn't exist.
func (h *inboxRoutes) checkTeam(w http.ResponseWriter, r *http.Request, body *teamBody) bool {
	if len(body.People) > 500 || len(body.Groups) > 100 {
		kit.Fail(w, r, http.StatusBadRequest, "A team takes at most 500 people and 100 user groups")
		return false
	}
	if body.People != nil {
		if _, ok := h.teamMembers(r.Context(), body.People); !ok {
			kit.Fail(w, r, http.StatusBadRequest, "You can't add one of these people")
			return false
		}
	}
	if body.Groups != nil {
		known, _, err := h.store().UserGroups(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return false
		}
		for _, g := range body.Groups {
			if !slices.ContainsFunc(known, func(k chatstore.TeamGroup) bool { return k.GUID == g }) {
				kit.Fail(w, r, http.StatusBadRequest, "One of these user groups doesn't exist")
				return false
			}
		}
	}
	return true
}

// newTeam answers POST /api/chat/team/teams {title, people, groups}.
func (h *inboxRoutes) newTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body teamBody
	if err := kit.Bind(r, &body); err != nil || strings.TrimSpace(body.Title) == "" {
		kit.Fail(w, r, http.StatusBadRequest, "A team needs a name")
		return
	}
	if !h.checkTeam(w, r, &body) {
		return
	}
	c, err := h.store().NewTeam(ctx, body.Title, kit.UserID(r), body.People, body.Groups)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	people, groups := h.teamState(ctx, c.ID)
	h.teamAudit(r, "chat_team_created", c, map[string]any{"people": h.peopleText(ctx, people), "groups": groupsText(groups)})
	h.teamChanged(ctx, c, nil)
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, "")})
}

// editTeam answers PATCH /api/chat/team/teams/{id} {title, people,
// groups}.
func (h *inboxRoutes) editTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if chatstore.KindOf(id) != chatstore.ConvTeam {
		kit.Fail(w, r, http.StatusNotFound, "Conversation not found")
		return
	}
	var body teamBody
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	if !h.checkTeam(w, r, &body) {
		return
	}
	before, _ := h.store().Members(ctx, id)
	old, _ := h.store().Conversation(ctx, id)
	oldPeople, oldGroups := h.teamState(ctx, id)
	c, err := h.store().SetTeam(ctx, id, body.Title, body.People, body.Groups)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	people, groups := h.teamState(ctx, id)
	changes := map[string]any{}
	if old.Title != c.Title {
		changes["renamed_from"] = old.Title
	}
	addChange(changes, "people_added", h.peopleText(ctx, idsWithout(people, oldPeople)))
	addChange(changes, "people_removed", h.peopleText(ctx, idsWithout(oldPeople, people)))
	addChange(changes, "groups_added", groupsText(withoutGroups(groups, oldGroups)))
	addChange(changes, "groups_removed", groupsText(withoutGroups(oldGroups, groups)))
	h.teamAudit(r, "chat_team_updated", c, changes)
	h.teamChanged(ctx, c, before)
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, "")})
}

// deleteTeam answers DELETE /api/chat/team/teams/{id}: the team and its
// messages are gone for everyone.
func (h *inboxRoutes) deleteTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	before, _ := h.store().Members(ctx, id)
	team, _ := h.store().Conversation(ctx, id)
	if err := h.store().DeleteTeam(ctx, id); err != nil {
		h.fail(w, r, err)
		return
	}
	team.ID = id
	h.teamAudit(r, "chat_team_deleted", team, nil)
	text := frame("conversation.removed", map[string]any{"conversation_id": id})
	for _, m := range before {
		h.hub.toAccount(m, text)
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true})
}

// teamChanged tells the team's members its new state, and the ones it
// lost that it is gone for them.
func (h *inboxRoutes) teamChanged(ctx context.Context, c chatstore.Conversation, before []int64) {
	now, err := h.store().Members(ctx, c.ID)
	if err != nil && !errors.Is(err, chatstore.ErrNotFound) {
		return
	}
	text := frame("conversation.updated", map[string]any{"conversation": conversationOut(c, "")})
	for _, m := range now {
		h.hub.toAccount(m, text)
	}
	gone := frame("conversation.removed", map[string]any{"conversation_id": c.ID})
	for _, m := range before {
		if !slices.Contains(now, m) {
			h.hub.toAccount(m, gone)
		}
	}
}

// teamAudit writes a team's audit entry by the signed-in account: the
// team's id and name, and what changed.
func (h *inboxRoutes) teamAudit(r *http.Request, action string, c chatstore.Conversation, changes map[string]any) {
	details := map[string]any{"conversation_id": c.ID, "title": c.Title}
	maps.Copy(details, changes)
	h.hub.audit(r.Context(), &member{user: h.user(r)}, action, details)
}

// teamState is who a team names by hand and which user groups it
// includes, for its audit entries.
func (h *inboxRoutes) teamState(ctx context.Context, id string) ([]int64, []chatstore.TeamGroup) {
	people, _ := h.store().TeamPeople(ctx, id)
	groups, _ := h.store().TeamGroups(ctx, id)
	return people, groups
}

// addChange notes a change for an audit entry when there is one.
func addChange(changes map[string]any, key, text string) {
	if text != "" {
		changes[key] = text
	}
}

// peopleText is accounts as an audit entry names them: "Ben Becker (6),
// cleo (8)".
func (h *inboxRoutes) peopleText(ctx context.Context, ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	known, _ := h.people(ctx)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		n := strconv.FormatInt(id, 10)
		if p, ok := known[id]; ok && p.Name != "" {
			n = p.Name + " (" + n + ")"
		}
		parts = append(parts, n)
	}
	return strings.Join(parts, ", ")
}

// groupsText is user groups as an audit entry names them: "Service desk
// (g-desk)".
func groupsText(groups []chatstore.TeamGroup) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, g.Name+" ("+g.GUID+")")
	}
	return strings.Join(parts, ", ")
}

// idsWithout are the ids of a that b lacks.
func idsWithout(a, b []int64) []int64 {
	var out []int64
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// withoutGroups are the user groups of a that b lacks.
func withoutGroups(a, b []chatstore.TeamGroup) []chatstore.TeamGroup {
	var out []chatstore.TeamGroup
	for _, g := range a {
		if !slices.ContainsFunc(b, func(k chatstore.TeamGroup) bool { return k.GUID == g.GUID }) {
			out = append(out, g)
		}
	}
	return out
}
