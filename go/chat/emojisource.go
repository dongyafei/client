package chat

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/keybase/client/go/chat/attachments"
	"github.com/keybase/client/go/chat/globals"
	"github.com/keybase/client/go/chat/types"
	"github.com/keybase/client/go/chat/utils"
	"github.com/keybase/client/go/protocol/chat1"
	"github.com/keybase/client/go/protocol/gregor1"
	"github.com/keybase/client/go/protocol/keybase1"
)

type DevConvEmojiSource struct {
	globals.Contextified
	utils.DebugLabeler

	getLock     sync.Mutex
	aliasLookup map[string]chat1.Emoji
	ri          func() chat1.RemoteInterface
}

var _ types.EmojiSource = (*DevConvEmojiSource)(nil)

func NewDevConvEmojiSource(g *globals.Context, ri func() chat1.RemoteInterface) *DevConvEmojiSource {
	return &DevConvEmojiSource{
		Contextified: globals.NewContextified(g),
		DebugLabeler: utils.NewDebugLabeler(g.ExternalG(), "DevConvEmojiSource", false),
		ri:           ri,
	}
}

func (s *DevConvEmojiSource) makeStorage() types.ConvConversationBackedStorage {
	return NewConvDevConversationBackedStorage(s.G(), chat1.TopicType_EMOJI, true, s.ri)
}

func (s *DevConvEmojiSource) topicName(suffix *string) string {
	ret := "emojis"
	if suffix != nil {
		ret += *suffix
	}
	return ret
}

func (s *DevConvEmojiSource) Add(ctx context.Context, uid gregor1.UID, convID chat1.ConversationID,
	alias, filename string, topicNameSuffix *string, versionOverride *chat1.EmojiMessageVersion) (res chat1.EmojiRemoteSource, err error) {
	defer s.Trace(ctx, func() error { return err }, "Add")()
	if strings.Contains(alias, "#") {
		return res, errors.New("invalid character in emoji alias")
	}
	var stored chat1.EmojiStorage
	alias = strings.ReplaceAll(alias, ":", "") // drop any colons from alias
	storage := s.makeStorage()
	topicName := s.topicName(topicNameSuffix)
	_, storageConv, err := storage.Get(ctx, uid, convID, topicName, &stored, true)
	if err != nil {
		return res, err
	}
	if stored.Mapping == nil {
		stored.Mapping = make(map[string]chat1.EmojiRemoteSource)
	}
	sender := NewBlockingSender(s.G(), NewBoxer(s.G()), s.ri)
	_, msgID, err := attachments.NewSender(s.G()).PostFileAttachment(ctx, sender, uid,
		storageConv.GetConvID(), storageConv.Info.TlfName, keybase1.TLFVisibility_PRIVATE, nil, filename,
		"", nil, 0, nil, nil)
	if err != nil {
		return res, err
	}
	if msgID == nil {
		return res, errors.New("no messageID from attachment")
	}
	version := chat1.EmojiMessageVersion(*msgID)
	if versionOverride != nil {
		version = *versionOverride
	}
	res = chat1.NewEmojiRemoteSourceWithMessage(chat1.EmojiMessage{
		ConvID:  storageConv.GetConvID(),
		MsgID:   *msgID,
		Version: version,
	})
	stored.Mapping[alias] = res
	return res, storage.Put(ctx, uid, convID, topicName, stored)
}

func (s *DevConvEmojiSource) remoteToLocalSource(ctx context.Context, remote chat1.EmojiRemoteSource) (res chat1.EmojiLoadSource, err error) {
	typ, err := remote.Typ()
	if err != nil {
		return res, err
	}
	switch typ {
	case chat1.EmojiRemoteSourceTyp_MESSAGE:
		msg := remote.Message()
		url := s.G().AttachmentURLSrv.GetURL(ctx, msg.ConvID, msg.MsgID, false)
		return chat1.NewEmojiLoadSourceWithHttpsrv(url), nil
	default:
		return res, errors.New("unknown remote source")
	}
}

func (s *DevConvEmojiSource) getNoSet(ctx context.Context, uid gregor1.UID, convID *chat1.ConversationID) (res chat1.UserEmojis, aliasLookup map[string]chat1.Emoji, err error) {
	aliasLookup = make(map[string]chat1.Emoji)
	storage := s.makeStorage()
	topicType := chat1.TopicType_EMOJI
	var sourceTLFID *chat1.TLFID
	seenAliases := make(map[string]int)
	if convID != nil {
		conv, err := utils.GetUnverifiedConv(ctx, s.G(), uid, *convID, types.InboxSourceDataSourceAll)
		if err != nil {
			return res, aliasLookup, err
		}
		sourceTLFID = new(chat1.TLFID)
		*sourceTLFID = conv.Conv.Metadata.IdTriple.Tlfid
	}
	ibox, _, err := s.G().InboxSource.Read(ctx, uid, types.ConversationLocalizerBlocking,
		types.InboxSourceDataSourceAll, nil, &chat1.GetInboxLocalQuery{
			TopicType:    &topicType,
			MemberStatus: chat1.AllConversationMemberStatuses(),
		})
	if err != nil {
		return res, aliasLookup, err
	}
	convs := ibox.Convs
	addEmojis := func(convs []chat1.ConversationLocal) {
		for _, conv := range convs {
			var stored chat1.EmojiStorage
			found, err := storage.GetFromKnownConv(ctx, uid, conv, &stored)
			if err != nil {
				s.Debug(ctx, "Get: failed to read from known conv: %s", err)
				continue
			}
			if !found {
				s.Debug(ctx, "Get: no stored info for: %s", conv.GetConvID())
				continue
			}
			group := chat1.EmojiGroup{
				Name: conv.Info.TlfName,
			}
			for alias, storedEmoji := range stored.Mapping {
				source, err := s.remoteToLocalSource(ctx, storedEmoji)
				if err != nil {
					s.Debug(ctx, "Get: skipping emoji on remote-to-local error: %s", err)
					continue
				}
				emoji := chat1.Emoji{
					Alias:        alias,
					Source:       source,
					RemoteSource: storedEmoji,
				}
				if seen, ok := seenAliases[alias]; ok {
					seenAliases[alias]++
					emoji.Alias += fmt.Sprintf("#%d", seen)
					aliasLookup[alias] = emoji
				} else {
					seenAliases[alias] = 2
				}
				group.Emojis = append(group.Emojis, emoji)
			}
			res.Emojis = append(res.Emojis, group)
		}
	}
	var tlfConvs, otherConvs []chat1.ConversationLocal
	for _, conv := range convs {
		if sourceTLFID != nil && conv.Info.Triple.Tlfid.Eq(*sourceTLFID) {
			tlfConvs = append(tlfConvs, conv)
		} else {
			otherConvs = append(otherConvs, conv)
		}
	}
	addEmojis(tlfConvs)
	addEmojis(otherConvs)
	return res, aliasLookup, nil
}

func (s *DevConvEmojiSource) Get(ctx context.Context, uid gregor1.UID, convID *chat1.ConversationID) (res chat1.UserEmojis, err error) {
	defer s.Trace(ctx, func() error { return err }, "Get")()
	var aliasLookup map[string]chat1.Emoji
	if res, aliasLookup, err = s.getNoSet(ctx, uid, convID); err != nil {
		return res, err
	}
	s.getLock.Lock()
	defer s.getLock.Unlock()
	s.aliasLookup = aliasLookup
	return res, nil
}

var emojiPattern = regexp.MustCompile(`(?::)([^:\s]+)(?::)`)

type emojiMatch struct {
	name     string
	position []int
}

func (s *DevConvEmojiSource) parse(ctx context.Context, body string) (res []emojiMatch) {
	body = utils.ReplaceQuotedSubstrings(body, false)
	hits := emojiPattern.FindAllStringSubmatchIndex(body, -1)
	for _, hit := range hits {
		if len(hit) < 4 {
			s.Debug(ctx, "parse: malformed hit: %d", len(hit))
			continue
		}
		res = append(res, emojiMatch{
			name:     body[hit[2]:hit[3]],
			position: []int{hit[0], hit[1]},
		})
	}
	return res
}

func (s *DevConvEmojiSource) stripAlias(alias string) string {
	return strings.Split(alias, "#")[0]
}

func (s *DevConvEmojiSource) versionMatch(l chat1.EmojiRemoteSource, r chat1.EmojiRemoteSource) bool {
	if !l.IsMessage() || !r.IsMessage() {
		return false
	}
	return l.Message().Version == r.Message().Version
}

func (s *DevConvEmojiSource) syncCrossTeam(ctx context.Context, uid gregor1.UID, emoji chat1.HarvestedEmoji,
	convID chat1.ConversationID) (res chat1.HarvestedEmoji, err error) {
	if !emoji.Source.IsMessage() {
		return res, errors.New("can only sync message remote source")
	}
	suffix := convID.String()
	var stored chat1.EmojiStorage
	storage := s.makeStorage()
	if _, _, err := storage.Get(ctx, uid, convID, s.topicName(&suffix), &stored, true); err != nil {
		return res, err
	}
	if stored.Mapping == nil {
		stored.Mapping = make(map[string]chat1.EmojiRemoteSource)
	}

	// check for a match
	stripped := s.stripAlias(emoji.Alias)
	if existing, ok := stored.Mapping[stripped]; ok {
		s.Debug(ctx, "syncCrossTeam: hit mapping")
		if s.versionMatch(existing, emoji.Source) {
			s.Debug(ctx, "syncCrossTeam: hit version, returning")
			return chat1.HarvestedEmoji{
				Alias:  emoji.Alias,
				Source: existing,
			}, nil
		}
	}

	// download from the original source
	sink, err := ioutil.TempFile("", "emoji")
	if err != nil {
		return res, err
	}
	defer os.Remove(sink.Name())
	if err := attachments.Download(ctx, s.G(), uid, emoji.Source.Message().ConvID,
		emoji.Source.Message().MsgID, sink, false, nil, s.ri); err != nil {
		return res, err
	}

	// add the source to the target storage area
	version := emoji.Source.Message().Version
	newSource, err := s.Add(ctx, uid, convID, stripped, sink.Name(), &suffix, &version)
	if err != nil {
		return res, err
	}
	return chat1.HarvestedEmoji{
		Alias:       emoji.Alias,
		Source:      newSource,
		IsCrossTeam: true,
	}, nil
}

func (s *DevConvEmojiSource) Harvest(ctx context.Context, body string, uid gregor1.UID,
	convID chat1.ConversationID, crossTeams map[string]chat1.HarvestedEmoji,
	mode types.EmojiSourceHarvestMode) (res []chat1.HarvestedEmoji, err error) {
	matches := s.parse(ctx, body)
	if len(matches) == 0 {
		return nil, nil
	}
	defer s.Trace(ctx, func() error { return err }, "Harvest: mode: %v", mode)()
	s.Debug(ctx, "Harvest: %d matches found", len(matches))
	emojis, _, err := s.getNoSet(ctx, uid, &convID)
	if err != nil {
		return res, err
	}
	if len(emojis.Emojis) == 0 {
		return nil, nil
	}
	group := emojis.Emojis[0] // only consider the first hit
	s.Debug(ctx, "Harvest: using %d emojis to search for matches", len(group.Emojis))

	groupMap := make(map[string]chat1.EmojiRemoteSource, len(group.Emojis))
	for _, emoji := range group.Emojis {
		groupMap[emoji.Alias] = emoji.RemoteSource
	}
	crossTeamMap := make(map[string]chat1.HarvestedEmoji)
	aliasMap := make(map[string]chat1.EmojiRemoteSource)
	switch mode {
	case types.EmojiSourceHarvestModeInbound:
		for _, emoji := range crossTeams {
			crossTeamMap[emoji.Alias] = emoji
		}
	case types.EmojiSourceHarvestModeOutbound:
		s.getLock.Lock()
		for alias, emoji := range s.aliasLookup {
			aliasMap[alias] = emoji.RemoteSource
		}
		s.getLock.Unlock()
	default:
		return nil, errors.New("unknown harvest mode")
	}
	for _, match := range matches {
		// try group map first
		if source, ok := groupMap[match.name]; ok {
			res = append(res, chat1.HarvestedEmoji{
				Alias:  match.name,
				Source: source,
			})
		} else if emoji, ok := crossTeamMap[match.name]; ok {
			// then known cross teams
			emoji.IsCrossTeam = true
			res = append(res, emoji)
		} else if source, ok := aliasMap[match.name]; ok {
			// then any aliases we know about from the last Get call
			newEmoji, err := s.syncCrossTeam(ctx, uid, chat1.HarvestedEmoji{
				Alias:  match.name,
				Source: source,
			}, convID)
			if err != nil {
				return res, err
			}
			res = append(res, newEmoji)
		}
	}
	return res, nil
}

func (s *DevConvEmojiSource) Decorate(ctx context.Context, body string, convID chat1.ConversationID,
	emojis []chat1.HarvestedEmoji) string {
	if len(emojis) == 0 {
		return body
	}
	matches := s.parse(ctx, body)
	if len(matches) == 0 {
		return body
	}
	defer s.Trace(ctx, func() error { return nil }, "Decorate")()
	emojiMap := make(map[string]chat1.EmojiRemoteSource, len(emojis))
	for _, emoji := range emojis {
		emojiMap[emoji.Alias] = emoji.Source
	}
	offset := 0
	added := 0
	for _, match := range matches {
		if source, ok := emojiMap[match.name]; ok {
			localSource, err := s.remoteToLocalSource(ctx, source)
			if err != nil {
				s.Debug(ctx, "Decorate: failed to get local source: %s", err)
				continue
			}
			body, added = utils.DecorateBody(ctx, body, match.position[0]+offset,
				match.position[1]-match.position[0],
				chat1.NewUITextDecorationWithEmoji(chat1.Emoji{
					Alias:  match.name,
					Source: localSource,
				}))
			offset += added
		}
	}
	return body
}
