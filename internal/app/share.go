package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/internal/sharing"
	"github.com/lbr77/lody-mygo/locales"
)

// Sharing a conversation, as Lody's share dialog does: a static copy of
// the conversation, and its sub-conversations if asked, is captured,
// uploaded and published, and anyone holding the link can read it. The
// link's credential is the publisher's alone, kept in the keychain; the
// server keeps only its hash. An agent can ask for a share too (from the
// CLI): the request shows on its conversation until approved, and the
// link then goes back to the agent sealed to its key.

// shareEntry is a share as the server manages it.
type shareEntry struct {
	ShareID             string             `json:"shareId"`
	RootSessionID       string             `json:"rootSessionId"`
	PublisherUserID     string             `json:"publisherUserId"`
	Status              string             `json:"status"` // draft, active, revoked
	Revision            float64            `json:"revision"`
	CredentialVersion   float64            `json:"credentialVersion"`
	CurrentDeploymentID string             `json:"currentDeploymentId"`
	DeploymentID        string             `json:"deploymentId"` // on a deployment begun
	Title               string             `json:"title"`
	SourceIDs           []sharing.SourceID `json:"sourceIds"`
	SelectedSourceIDs   []string           `json:"selectedSourceIds"`
	CanManage           bool               `json:"canManage"`
	CanRevoke           bool               `json:"canRevoke"`
}

// shareRequest is an agent's request to share.
type shareRequest struct {
	RequestID         string   `json:"requestId"`
	SourceSessionID   string   `json:"sourceSessionId"`
	SessionIDs        []string `json:"sessionIds"`
	Purpose           string   `json:"purpose"`
	DeliveryPublicKey string   `json:"deliveryPublicKey"`
	ExpiresAt         float64  `json:"expiresAt"`
	Status            string   `json:"status"` // pending, confirmed, cancelled, expired, published
}

// shareConfirmation is the request a publication answers.
type shareConfirmation struct {
	requestID  string
	sessionIDs []string
	publicKey  string
}

// pendingShare is a frozen package and its credentials, kept to retry.
type pendingShare struct {
	prepared     *sharing.Prepared
	requestID    string
	uploadSecret string
	readerSecret string // "" when updating a share whose link stays
	expected     *shareEntry
	deployment   *shareEntry
	sealed       bool
	delivery     *sharing.Envelope
}

// shareJob publishes one conversation's share.
type shareJob struct {
	root     string
	confirm  *shareConfirmation
	entry    *shareEntry
	loaded   bool
	loadErr  string
	selected []string // nil: what the share holds, or the conversation alone
	pending  *pendingShare
	phase    string // capturing, uploading, publishing
	progress int
	busy     bool
	err      string
	notice   string
	result   *shareResult
}

type shareResult struct {
	url    string
	copied bool
}

// shareDialog is the dialog over a conversation.
type shareDialog struct {
	open       bool
	job        *shareJob
	confirming string // reset or revoke
	confirmRev float64
	copiedAt   time.Time
}

// openShare opens the dialog for a conversation.
func (a *App) openShare(root string) {
	job := &shareJob{root: root}
	a.share = shareDialog{open: true, job: job}
	a.loadShare(job)
}

// loadShare reads the share the conversation has, if any.
func (a *App) loadShare(job *shareJob) {
	if job.confirm != nil {
		// Each approval publishes a share of its own.
		job.loaded = true
		return
	}
	if a.convex == nil || a.offline {
		return
	}
	convex, ws, root := a.convex, a.workspaceID, job.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var entry *shareEntry
		err := convex.Query(ctx, "sessionSharing:getManagement", map[string]any{"workspaceId": ws, "rootSessionId": root}, &entry)
		a.update(func() {
			if err != nil {
				job.loadErr = err.Error()
				return
			}
			job.entry, job.loaded, job.loadErr = entry, true, ""
		})
	}()
}

// shareCandidates are the conversation and those opened from it, nearest
// first, as Lody offers them.
func shareCandidates(root string, sessions []catalog.Session) []catalog.Session {
	var out []catalog.Session
	ids := map[string]bool{root: true}
	queue := []string{root}
	for _, s := range sessions {
		if s.ID == root {
			out = append(out, s)
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, s := range sessions {
			if ids[s.ID] || s.ParentSessionID != queue[i] && s.OpenedBySessionID != queue[i] {
				continue
			}
			ids[s.ID] = true
			queue = append(queue, s.ID)
			out = append(out, s)
		}
	}
	return out
}

func (job *shareJob) selection() []string {
	switch {
	case job.confirm != nil:
		return job.confirm.sessionIDs
	case job.selected != nil:
		return job.selected
	case job.entry != nil && job.entry.Status == "active" && len(job.entry.SelectedSourceIDs) > 0:
		return job.entry.SelectedSourceIDs
	}
	return []string{job.root}
}

// canCapture is whether the selection can be captured now.
func (a *App) canCapture(job *shareJob) bool {
	sel := job.selection()
	if len(sel) == 0 || len(sel) > sharing.MaxConversations || !job.loaded {
		return false
	}
	cands := map[string]bool{}
	for _, s := range shareCandidates(job.root, a.catalog.Sessions) {
		cands[s.ID] = true
	}
	seen, root := map[string]bool{}, false
	for _, id := range sel {
		if seen[id] || !cands[id] {
			return false
		}
		seen[id], root = true, root || id == job.root
	}
	return root
}

// The keychain holds each share's reader secret, by its version.

type savedSecret struct {
	Secret  string  `json:"secret"`
	Version float64 `json:"credentialVersion"`
}

func (a *App) shareSecretKey(shareID string) string {
	user := ""
	if a.account != nil {
		user = a.account.User.ID
	}
	return "share/" + user + "/" + a.workspaceID + "/" + shareID
}

func readShareSecret(key string, version float64) string {
	b, err := mygo.Secrets.Get(key)
	var s savedSecret
	if err != nil || json.Unmarshal(b, &s) != nil || s.Version != version || !sharing.IsSecret(s.Secret) {
		return ""
	}
	return s.Secret
}

// saveShareSecret keeps a secret and reads it back, as a publication may
// only go ahead once its link can be rebuilt here.
func saveShareSecret(key string, version float64, secret string) error {
	b, _ := json.Marshal(savedSecret{secret, version})
	if mygo.Secrets.Set(key, b) != nil || readShareSecret(key, version) != secret {
		return errors.New(locales.ShareCredentialStorageFailed())
	}
	return nil
}

// linkSecret reads the secret a share's link is built with; tests swap
// it for the keychain.
var linkSecret = readShareSecret

// shareLink is the share's link, when this device holds its secret.
func (a *App) shareLink(e *shareEntry) string {
	if e == nil || e.Status != "active" || a.account == nil || e.PublisherUserID != a.account.User.ID {
		return ""
	}
	secret := linkSecret(a.shareSecretKey(e.ShareID), e.CredentialVersion)
	if secret == "" {
		return ""
	}
	url, _ := sharing.URL(e.ShareID, secret)
	return url
}

// publishShare captures, uploads and publishes, resuming a frozen package
// after a failure.
func (a *App) publishShare(job *shareJob) {
	if job.busy || !a.canCapture(job) || a.conn == nil || a.convex == nil {
		return
	}
	entry, selected, pending, confirm := job.entry, job.selection(), job.pending, job.confirm
	byID := map[string]catalog.Session{}
	for _, s := range shareCandidates(job.root, a.catalog.Sessions) {
		byID[s.ID] = s
	}
	conn, convex, api, token, ws, root := a.conn, a.convex, a.api, a.tokenNow(), a.workspaceID, job.root
	key := a.shareSecretKey
	omission := locales.ShareFileOmitted()
	job.busy, job.err, job.notice = true, "", ""
	setPhase := func(phase string, progress int) {
		a.update(func() { job.phase, job.progress = phase, progress })
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var updated shareEntry
		err := func() error {
			if pending == nil {
				if entry != nil && entry.Status == "draft" && entry.CanManage {
					// A stale draft can't be resumed: it goes before a new deployment.
					if err := convex.Mutation(ctx, "sessionSharing:revoke", map[string]any{"shareId": entry.ShareID, "expectedRevision": entry.Revision}, nil); err != nil {
						return err
					}
					entry = nil
				}
				setPhase("capturing", 0)
				var sources []sharing.Source
				for _, id := range selected {
					s := byID[id]
					history, err := session.ReadHistory(ctx, conn, id)
					if err != nil {
						return err
					}
					sources = append(sources, sharing.Source{SourceID: id, Title: s.Title, History: history, ParentSourceID: s.ParentSessionID, OpenedBySourceID: s.OpenedBySessionID})
				}
				var previous []sharing.SourceID
				if confirm == nil && entry != nil {
					previous = entry.SourceIDs
				}
				prepared, err := sharing.Prepare(ctx, sharing.Options{
					RootSourceID: root, Sources: sources, Previous: previous, CapturedAt: time.Now(), OmissionText: omission,
					ReadAttachment: func(ctx context.Context, at sharing.AttachmentSource) ([]byte, string, error) {
						if at.Kind != "image" {
							return nil, "", errors.New("share_file_disabled")
						}
						storage, _ := at.Reference["storageSessionId"].(string)
						storage = firstNonEmpty(storage, at.ConversationSourceID)
						image, _ := at.Reference["imageId"].(string)
						if !sharing.ValidID(storage) || !sharing.ValidID(image) {
							return nil, "", errors.New("invalid share attachment")
						}
						return api.SessionImageOriginal(ctx, token, ws, storage, image, sharing.MaxImageBytes)
					},
				})
				if err != nil {
					return err
				}
				pending = &pendingShare{prepared: prepared, requestID: uuid.NewString(), uploadSecret: sharing.NewSecret()}
				if confirm == nil && entry != nil && entry.Status == "active" {
					pending.expected = entry
				} else {
					pending.readerSecret = sharing.NewSecret()
				}
				p := pending
				a.update(func() { job.pending = p })
			}
			if confirm != nil && pending.delivery == nil {
				env, err := sharing.EncryptDelivery(confirm.publicKey, confirm.requestID, sharing.Origin, pending.readerSecret)
				if err != nil {
					return err
				}
				pending.delivery = env
			}
			setPhase("uploading", 0)
			if pending.deployment == nil {
				uploadHash, _ := sharing.HashSecret(pending.uploadSecret)
				args := map[string]any{
					"workspaceId": ws, "rootSessionId": root, "uploadCredentialHash": uploadHash, "requestId": pending.requestID,
					"manifest": pending.prepared.Manifest, "sourceIds": pending.prepared.SourceIDs,
				}
				if e := pending.expected; e != nil {
					args["shareId"], args["expectedRevision"] = e.ShareID, e.Revision
				}
				if pending.readerSecret != "" {
					args["credentialHash"], _ = sharing.HashSecret(pending.readerSecret)
				}
				if confirm != nil {
					args["confirmationRequestId"], args["delivery"] = confirm.requestID, pending.delivery
				}
				var dep shareEntry
				if err := convex.Mutation(ctx, "sessionSharing:beginDeployment", args, &dep); err != nil {
					return err
				}
				if !sharing.ValidID(dep.DeploymentID) {
					return errors.New("share_invalid_response")
				}
				pending.deployment = &dep
			}
			dep := pending.deployment
			if pending.readerSecret != "" {
				if err := saveShareSecret(key(dep.ShareID), dep.CredentialVersion, pending.readerSecret); err != nil {
					return err
				}
			}
			if !pending.sealed {
				err := sharing.Upload(ctx, nil, dep.DeploymentID, pending.uploadSecret, pending.prepared, func(done, total int) {
					pct := 100
					if total > 0 {
						pct = int(math.Round(float64(done) / float64(total) * 100))
					}
					setPhase("uploading", pct)
				})
				if err != nil {
					return err
				}
				pending.sealed = true
			}
			setPhase("publishing", 0)
			if err := convex.Mutation(ctx, "sessionSharing:publishDeployment", map[string]any{"deploymentId": dep.DeploymentID}, &updated); err != nil {
				return err
			}
			if updated.Status != "active" || updated.CurrentDeploymentID != dep.DeploymentID {
				return errors.New("share_not_published")
			}
			return nil
		}()
		a.update(func() {
			job.busy, job.phase = false, ""
			if err != nil {
				job.err = shareError(err)
				return
			}
			url := a.shareLink(&updated)
			copied := false
			if url != "" {
				mygo.Clipboard.WriteText(url)
				copied = true
			}
			job.result = &shareResult{url, copied}
			job.pending, job.selected, job.progress = nil, nil, 0
			job.entry = &updated
			a.refetchShares()
			if job.confirm != nil {
				a.refetchKey("share-requests/" + job.root)
			}
		})
	}()
}

// shareError says what went wrong, in the app's words where it knows.
func shareError(err error) string {
	switch err.Error() {
	case "share_file_disabled":
		return locales.ShareFileOmitted()
	case "share_invalid_response", "share_not_published":
		return locales.ShareFailed()
	}
	return err.Error()
}

// resetShareLink replaces the link: the old one stops working.
func (a *App) resetShareLink(job *shareJob) {
	e := job.entry
	if e == nil || job.busy || !e.CanManage || e.Status != "active" || a.convex == nil {
		return
	}
	convex, key := a.convex, a.shareSecretKey(e.ShareID)
	job.busy, job.err, job.notice = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var updated shareEntry
		err := func() error {
			// Kept first under the next version, so a crash can't lose it.
			secret := readShareSecret(key, e.CredentialVersion+1)
			if secret == "" {
				secret = sharing.NewSecret()
			}
			if err := saveShareSecret(key, e.CredentialVersion+1, secret); err != nil {
				return err
			}
			hash, _ := sharing.HashSecret(secret)
			if err := convex.Mutation(ctx, "sessionSharing:resetCredential", map[string]any{"shareId": e.ShareID, "expectedRevision": e.Revision, "credentialHash": hash}, &updated); err != nil {
				return err
			}
			return saveShareSecret(key, updated.CredentialVersion, secret)
		}()
		a.update(func() {
			job.busy = false
			if err != nil {
				job.err = shareError(err)
				return
			}
			updated.SourceIDs, updated.SelectedSourceIDs, updated.CanManage, updated.CanRevoke = e.SourceIDs, e.SelectedSourceIDs, e.CanManage, e.CanRevoke
			job.entry, job.result = &updated, nil
			a.refetchShares()
		})
	}()
}

// revokeShare takes the share down; its link stops working.
func (a *App) revokeShare(job *shareJob) {
	e := job.entry
	if e == nil || job.busy || !e.CanRevoke || a.convex == nil {
		return
	}
	convex, key := a.convex, a.shareSecretKey(e.ShareID)
	job.busy, job.err, job.notice = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var updated shareEntry
		err := convex.Mutation(ctx, "sessionSharing:revoke", map[string]any{"shareId": e.ShareID, "expectedRevision": e.Revision}, &updated)
		if err == nil {
			mygo.Secrets.Delete(key)
		}
		a.update(func() {
			job.busy = false
			if err != nil {
				job.err = shareError(err)
				return
			}
			job.entry, job.result, job.pending = &updated, nil, nil
			a.refetchShares()
		})
	}()
}

func (a *App) shareDialogView(c *ui.Context, p *palette) {
	d := &a.share
	if !d.open || d.job == nil {
		return
	}
	job := d.job
	open := true
	title := locales.UntitledSession()
	if s := a.catalog.Session(job.root); s != nil && s.Title != "" {
		title = s.Title
	}
	sheet(c, p, &open, 480, func() {
		ui.Row(c).FillWidth().Padding(18, 14, 8, 20).Gap(12).AlignItems(ui.Start).Shrink(0).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, locales.ShareTitle()).FontSize(sizeFoot).TextColor(p.Secondary)
				ui.Text(c, title).FontSize(16).FontWeight(600).TextColor(p.Strong).SingleLine()
			})
			iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { open = false })
		})
		step := a.shareStep(job, d)
		// Lody's StepTransition: the body's height follows what it holds
		// over 300ms, easing out, clipped on the way, and each step arrives
		// from 4px to the right as it fades in.
		frame := ui.Column(c.Key("share-height")).FillWidth().Shrink(0).ClipY()
		var inner ui.Element
		frame.Children(func() {
			inner = ui.Column(c.Key("share-inner")).FillWidth().Shrink(0).Children(func() {
				body := ui.Column(c.Key("share-step-"+step)).FillWidth().Padding(4, 20, 8, 20).Gap(12).
					Transition(ui.ElementTransition{Duration: durRegular, Ease: easeLody, Enter: &ui.Motion{X: 4}})
				body.Children(func() { a.shareBody(c, p, job, d, step) })
			})
		})
		// The height the content had in the last frame: none as the dialog
		// first lays it out, which shows at its height.
		if h := inner.Bounds().H; h > 0 {
			frame.Height(frame.AnimateWith("height", h, 300*time.Millisecond, cssEaseOut))
		}
		ui.Row(c).FillWidth().Padding(8, 20, 16, 20).Gap(8).AlignItems(ui.Center).Children(func() { a.shareFooter(c, p, job, d, step) })
	})
	if !open && !job.busy {
		d.open = false
	}
}

// shareStep is which screen the dialog shows.
func (a *App) shareStep(job *shareJob, d *shareDialog) string {
	switch {
	case d.confirming != "":
		return "confirm"
	case !job.loaded && job.loadErr == "":
		return "loading"
	case job.phase != "":
		return "publishing"
	case job.result != nil:
		return "published"
	}
	return "setup"
}

func note(c *ui.Context, p *palette, text string, ink ui.Color) {
	ui.Text(c, text).FontSize(sizeFoot).LineHeight(1.45).TextColor(ink).Selectable()
}

func (a *App) shareBody(c *ui.Context, p *palette, job *shareJob, d *shareDialog, step string) {
	entry := job.entry
	active := entry != nil && entry.Status == "active"
	switch step {
	case "confirm":
		title := locales.ShareRevoke()
		if d.confirming == "reset" {
			title = locales.ShareReset()
		}
		ui.Text(c, title).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
		note(c, p, locales.ShareInvalidateNotice(), p.Secondary)
		return
	case "loading":
		skeleton(c, p, "share-1", 16).WidthPercent(75)
		skeleton(c, p, "share-2", 12).FillWidth()
		skeleton(c, p, "share-3", 12).WidthPercent(66)
		return
	case "publishing":
		label := locales.SharePhaseCapturing()
		switch job.phase {
		case "uploading":
			label = locales.SharePhaseUploading(job.progress)
		case "publishing":
			label = locales.SharePhasePublishing()
		}
		ui.Text(c, label).FontSize(sizeBody).TextColor(p.Text)
		progressBar(c, p, job.phase == "uploading", job.progress)
		note(c, p, locales.SharePhaseHint(), p.Secondary)
		return
	case "published":
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			publishedGlyph(c, p, 32)
			line := locales.SharePublished()
			if job.result.copied {
				line = locales.SharePublishedCopied()
			}
			ui.Text(c, line).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
		})
		if job.result.url != "" {
			a.shareLinkField(c, p, job.result.url, d)
		} else {
			note(c, p, locales.ShareSecretMissing(), p.Secondary)
		}
		if job.err != "" {
			note(c, p, job.err, p.Danger)
		}
		return
	}
	if job.loadErr != "" {
		note(c, p, job.loadErr, p.Danger)
	}
	canPublish := entry == nil || entry.CanManage || entry.Status == "revoked"
	ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
		if active {
			ui.Icon(c, iconGlobe).Size(16, 16).TextColor(p.Secondary).Margin(1, 0, 0, 0)
		}
		note(c, p, locales.SharePublicNotice(), p.Text)
	})
	if link := a.shareLink(entry); active && link != "" {
		a.shareLinkField(c, p, link, d)
	}
	if entry != nil && entry.Status == "revoked" {
		note(c, p, locales.ShareRevokedNotice(), p.Secondary)
	}
	if !canPublish {
		note(c, p, locales.ShareOtherPublisher(), p.Secondary)
	}
	if active && canPublish {
		note(c, p, locales.ShareUpdateNotice(), p.Secondary)
	}
	children := shareCandidates(job.root, a.catalog.Sessions)
	if len(children) > 0 {
		children = children[1:]
	}
	shareable := children[:min(len(children), sharing.MaxConversations-1)]
	if len(children) > 0 && canPublish && job.confirm == nil {
		sel := map[string]bool{}
		for _, id := range job.selection() {
			sel[id] = true
		}
		picked := 0
		for _, s := range shareable {
			if sel[s.ID] {
				picked++
			}
		}
		on := picked > 0
		label := locales.ShareIncludeChildren(len(shareable))
		if picked > 0 && picked < len(shareable) {
			label = locales.ShareIncludeChildrenPartial(picked, len(shareable))
		}
		before := on
		box := ui.Checkbox(c, &on, label).Disabled(job.busy)
		if picked > 0 && picked < len(shareable) {
			box.Mixed()
		}
		if on != before {
			ids := []string{job.root}
			if on {
				for _, s := range shareable {
					ids = append(ids, s.ID)
				}
			}
			job.selected, job.pending = ids, nil
		}
	}
	if len(children) >= sharing.MaxConversations {
		note(c, p, locales.ShareLimit(sharing.MaxConversations), p.Secondary)
	}
	note(c, p, locales.ShareAttachmentNotice(), p.Secondary)
	if !a.canCapture(job) && canPublish && job.loaded {
		note(c, p, locales.ShareSourceUnavailable(), p.Secondary)
	}
	if active && entry.CanManage && a.shareLink(entry) == "" {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.ShareSecretMissing()).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
			outlineButton(c, p, nil, locales.ShareReset()).OnClick(func() { d.confirming, d.confirmRev = "reset", entry.Revision })
		})
	}
	if job.err != "" {
		note(c, p, job.err, p.Danger)
	}
	if job.notice != "" {
		note(c, p, job.notice, p.Secondary)
	}
}

// shareLinkField shows a link with its copy button.
func (a *App) shareLinkField(c *ui.Context, p *palette, url string, d *shareDialog) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Height(28).Padding(0, 10).Radius(8).AlignItems(ui.Center).Background(p.Text.Alpha(0.05)).Children(func() {
			ui.Text(c, url).Font(fontMono).FontSize(sizeCaption).TextColor(p.Text).SingleLine().Selectable().Label(locales.ShareLinkLabel())
		})
		copyButton(c, p, "share-link", &d.copiedAt, locales.ShareCopy(), locales.ReferralCopied(), func() { mygo.Clipboard.WriteText(url) })
	})
}

func (a *App) shareFooter(c *ui.Context, p *palette, job *shareJob, d *shareDialog, step string) {
	entry := job.entry
	active := entry != nil && entry.Status == "active"
	revoke := func() {
		if entry != nil && entry.CanRevoke {
			b := dangerGhostButton(c, p, locales.ShareRevoke()).Disabled(job.busy)
			b.OnClick(func() { d.confirming, d.confirmRev = "revoke", entry.Revision })
		}
	}
	switch step {
	case "loading", "publishing":
		return
	case "confirm":
		ui.Spacer(c)
		ghostButton(c, p, locales.Cancel()).OnClick(func() { d.confirming = "" })
		ok := !job.busy && entry != nil && d.confirmRev == entry.Revision
		// The destructive action sinks a pixel while held, as Lody's.
		b := pressable(ghostButton(c, p, locales.ShareConfirm()).Border(1, p.Danger.Alpha(0.6)), 0.97)
		if !ok {
			b.Disabled(true).Opacity(0.45)
		}
		b.OnClick(func() {
			if d.confirming == "reset" {
				a.resetShareLink(job)
			} else {
				a.revokeShare(job)
			}
			d.confirming = ""
		})
		return
	case "published":
		revoke()
		ui.Spacer(c)
		url := job.result.url
		primaryButton(c, p, locales.ShareCopy(), url != "" && !job.busy).OnClick(func() {
			mygo.Clipboard.WriteText(url)
			a.toast("success", locales.ShareCopied())
		})
		return
	}
	revoke()
	ui.Spacer(c)
	if link := a.shareLink(entry); active && link != "" {
		pressable(ghostButton(c, p, locales.ShareCopy()).Border(1, p.Border), 0.97).OnClick(func() {
			mygo.Clipboard.WriteText(link)
			job.notice = locales.ShareCopied()
		})
	}
	canPublish := entry == nil || entry.CanManage || entry.Status == "revoked"
	if !canPublish {
		pressable(ghostButton(c, p, locales.Close()).Border(1, p.Border), 0.97).OnClick(func() { d.open = false })
		return
	}
	if !active {
		ghostButton(c, p, locales.Cancel()).OnClick(func() { d.open = false })
	}
	label := locales.ShareConversation()
	switch {
	case job.err != "" && job.pending != nil:
		label = locales.PairingRetry()
	case active:
		label = locales.ShareUpdate()
	}
	// Busy, Lody's buttons only disable: no spinner turns beside them.
	primaryButton(c, p, label, !job.busy && a.canCapture(job)).OnClick(func() { a.publishShare(job) })
}

// progressBar is Lody's: a bar filled to the percent, or, without one, a
// 40% band crossing the track every 1.4s.
func progressBar(c *ui.Context, p *palette, determinate bool, percent int) {
	track := ui.Row(c.Key("progress")).FillWidth().Height(6).Radius(3).Background(p.Text.Alpha(0.06)).ClipX()
	track.Children(func() {
		if determinate {
			w := track.AnimateWith("fill", float32(percent), durRegular, easeLody)
			ui.Box(c).WidthPercent(w).Height(6).Radius(3).Background(p.Primary)
			return
		}
		band := ui.Box(c.Key("band")).WidthPercent(40).Height(6).Radius(3).Background(p.Primary)
		k := band.Keyframes("sweep", []ui.Keyframe{{Value: 0}, {At: 1400 * time.Millisecond, Value: 1, Ease: easeLody}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
		w := track.Bounds().W * 0.4
		band.Translate(w*(-1+3.5*k), 0)
	})
}

// publishedGlyph is Lody's mark for a share published: the ring closes
// over 450ms, the tick follows from 350ms, and one ripple leaves.
func publishedGlyph(c *ui.Context, p *palette, size float32) {
	box := ui.Box(c.Key("published-glyph")).Size(size, size).Shrink(0)
	ring := box.Keyframes("ring", []ui.Keyframe{{Value: 0}, {At: 450 * time.Millisecond, Value: 1, Ease: easeLody}})
	tick := box.Keyframes("tick", []ui.Keyframe{{Value: 0}, {At: 350 * time.Millisecond, Value: 0}, {At: 650 * time.Millisecond, Value: 1, Ease: easeLody}})
	ripple := box.Keyframes("ripple", []ui.Keyframe{{Value: 0}, {At: 300 * time.Millisecond, Value: 0}, {At: time.Second, Value: 1, Ease: easeLody}})
	ink := p.Success
	box.Draw(func(pt *ui.Painter, r ui.Rect) {
		s := r.W / 32
		cx, cy := r.X+16*s, r.Y+16*s
		circle := func(radius, sweep float32) [][2]float32 {
			n := max(2, int(64*sweep))
			pts := make([][2]float32, 0, n+1)
			for i := 0; i <= n; i++ {
				a := -math.Pi/2 + 2*math.Pi*float64(sweep)*float64(i)/float64(n)
				pts = append(pts, [2]float32{cx + radius*float32(math.Cos(a)), cy + radius*float32(math.Sin(a))})
			}
			return pts
		}
		if ripple > 0 && ripple < 1 {
			scale := 0.8 + 0.55*ripple
			pt.StrokePolyline(circle(12*s*scale, 1), 1*s, ink.Alpha(0.35*(1-ripple)))
		}
		if ring > 0 {
			pt.StrokePolyline(circle(11*s, ring), 1.75*s, ink)
		}
		if tick > 0 {
			// The tick, drawn along its length.
			pts := [][2]float32{{10.8, 16.4}, {14.5, 20.1}, {21.4, 12.5}}
			l1 := math.Hypot(3.7, 3.7)
			l2 := math.Hypot(6.9, 7.6)
			at := float64(tick) * (l1 + l2)
			line := [][2]float32{{r.X + pts[0][0]*s, r.Y + pts[0][1]*s}}
			if at <= l1 {
				f := float32(at / l1)
				line = append(line, [2]float32{r.X + (pts[0][0]+(pts[1][0]-pts[0][0])*f)*s, r.Y + (pts[0][1]+(pts[1][1]-pts[0][1])*f)*s})
			} else {
				f := float32((at - l1) / l2)
				line = append(line, [2]float32{r.X + pts[1][0]*s, r.Y + pts[1][1]*s},
					[2]float32{r.X + (pts[1][0]+(pts[2][0]-pts[1][0])*f)*s, r.Y + (pts[1][1]+(pts[2][1]-pts[1][1])*f)*s})
			}
			pt.StrokePolyline(line, 1.75*s, ink)
		}
	})
}

// pendingShareRequests are an agent's open requests on the conversation.
func (a *App) pendingShareRequests(c *ui.Context, sessionID string) []shareRequest {
	var out []shareRequest
	for _, r := range a.shareRequests(c, sessionID) {
		if job := a.shareJobs[r.RequestID]; (r.Status == "pending" || r.Status == "confirmed") && (job == nil || job.result == nil) {
			out = append(out, r)
		}
	}
	return out
}

// shareRequests are an agent's requests on the conversation.
func (a *App) shareRequests(c *ui.Context, sessionID string) []shareRequest {
	if a.convex == nil {
		return nil
	}
	convex, ws := a.convex, a.workspaceID
	key := "share-requests/" + sessionID
	f := a.fetch(key, func(ctx context.Context) (any, error) {
		var out []shareRequest
		err := convex.Query(ctx, "sessionSharing:listRequests", map[string]any{"workspaceId": ws, "sourceSessionId": sessionID}, &out)
		return out, err
	})
	// Requests arrive while the conversation is open: look again now and then.
	if a.shareReqAt == nil {
		a.shareReqAt = map[string]time.Time{}
	}
	if !a.offline && !f.loading && time.Since(a.shareReqAt[sessionID]) > 15*time.Second {
		a.shareReqAt[sessionID] = time.Now()
		if f.value != nil || f.err != "" {
			a.reload(f, nil)
		}
	}
	c.After(15 * time.Second)
	out, _ := f.value.([]shareRequest)
	return out
}

// shareRequestCards show an agent's requests to share, above the composer.
func (a *App) shareRequestCards(c *ui.Context, p *palette, sessionID string, requests []shareRequest) {
	for _, r := range requests {
		if r.Status != "pending" && r.Status != "confirmed" {
			continue
		}
		req := r
		if a.shareJobs == nil {
			a.shareJobs = map[string]*shareJob{}
		}
		job := a.shareJobs[r.RequestID]
		if job == nil {
			job = &shareJob{root: sessionID, confirm: &shareConfirmation{requestID: r.RequestID, sessionIDs: r.SessionIDs, publicKey: r.DeliveryPublicKey}}
			a.shareJobs[r.RequestID] = job
			a.loadShare(job)
		}
		if job.result != nil {
			continue
		}
		card := ui.Column(c.Key("share-req-"+r.RequestID)).FillWidth().Gap(12).Padding(16).Radius(14).Background(p.Composer).Border(0.5, p.ComposerBorder).
			Transition(ui.ElementTransition{Duration: durSlow, Ease: easeLody, Enter: &ui.Motion{Y: 8}})
		card.Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconShare).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, locales.ShareRequestTitle()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			ui.Text(c, locales.ShareRequestPurpose(req.Purpose)).FontSize(sizeBody).FontWeight(500).TextColor(p.Text).Selectable()
			note(c, p, locales.ShareRequestDisclosure(), p.Text)
			ui.Column(c).FillWidth().Gap(2).Padding(8, 10).Radius(8).Background(p.Text.Alpha(0.04)).Children(func() {
				for _, id := range req.SessionIDs {
					title := locales.UntitledSession()
					if s := a.catalog.Session(id); s != nil && s.Title != "" {
						title = s.Title
					}
					ui.Row(c.Key(id)).Gap(8).Children(func() {
						ui.Text(c, title).FontSize(sizeFoot).TextColor(p.Text).SingleLine()
						ui.Text(c, id).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
					})
				}
			})
			if job.busy {
				line := locales.ShareRequestPublishing()
				if job.phase == "uploading" {
					line += fmt.Sprintf(" %d%%", job.progress)
				}
				ui.Text(c, line).FontSize(sizeFoot).TextColor(p.Secondary)
			}
			if job.err != "" {
				note(c, p, job.err, p.Danger)
			}
			if req.Status == "confirmed" && job.pending == nil && !job.busy {
				note(c, p, locales.ShareRequestIncomplete(), p.Warning)
			}
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				deny := locales.ShareRequestDeny()
				if req.Status == "confirmed" {
					deny = locales.ShareRequestAbandon()
				}
				ghostButton(c, p, deny).Disabled(job.busy).OnClick(func() { a.cancelShareRequest(req.RequestID, sessionID, job) })
				if req.Status == "pending" || job.pending != nil {
					label := locales.ShareRequestApprove()
					if job.pending != nil {
						label = locales.PairingRetry()
					}
					primaryButton(c, p, label, !job.busy && a.canCapture(job)).OnClick(func() { a.publishShare(job) })
				}
			})
		})
	}
}

func (a *App) cancelShareRequest(requestID, sessionID string, job *shareJob) {
	if a.convex == nil || job.busy {
		return
	}
	convex := a.convex
	job.busy, job.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := convex.Mutation(ctx, "sessionSharing:cancelRequest", map[string]any{"requestId": requestID}, nil)
		a.update(func() {
			job.busy = false
			if err != nil {
				job.err = locales.ShareRequestFailed()
				return
			}
			a.refetchKey("share-requests/" + sessionID)
		})
	}()
}
