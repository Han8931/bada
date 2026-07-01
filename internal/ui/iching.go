package ui

import (
	"hash/fnv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type ichingFortune struct {
	Number  int
	Symbol  string
	Name    string
	Theme   string
	Advice  string
	Caution string
}

var ichingFortunes = []ichingFortune{
	{1, "䷀", "Qian / The Creative", "initiative, clarity, and strong forward motion", "Begin boldly, but keep your purpose clean and simple.", "Strength becomes brittle when it refuses to listen."},
	{2, "䷁", "Kun / The Receptive", "patience, support, and fertile ground", "Make room for things to unfold; cooperation carries you farther today.", "Do not mistake waiting for giving up."},
	{3, "䷂", "Zhun / Difficulty at the Beginning", "early friction before growth", "Start small, organize the basics, and ask for help where needed.", "Avoid forcing a new path before it has roots."},
	{4, "䷃", "Meng / Youthful Folly", "learning through humility", "Approach the day as a student; one sincere question opens a door.", "Pretending to know will cost more than admitting uncertainty."},
	{5, "䷄", "Xu / Waiting", "timing, nourishment, and trust", "Prepare while you wait. The pause is part of the answer.", "Impatience may push you into avoidable trouble."},
	{6, "䷅", "Song / Conflict", "tension, boundaries, and careful speech", "Clarify your position without escalating the dispute.", "Winning the argument may still damage the relationship."},
	{7, "䷆", "Shi / The Army", "discipline and coordinated effort", "Choose one priority and lead yourself with steadiness.", "Scattered action weakens even a strong plan."},
	{8, "䷇", "Bi / Holding Together", "belonging, alliance, and mutual trust", "Reach toward people whose values align with yours.", "Do not cling to a group that asks you to shrink."},
	{9, "䷈", "Xiao Chu / Small Taming", "gentle restraint and incremental progress", "Handle the small details; quiet consistency matters today.", "A tiny leak becomes large if ignored."},
	{10, "䷉", "Lu / Treading", "careful conduct in sensitive territory", "Move respectfully and watch where your words land.", "Confidence without tact can step on a tail."},
	{11, "䷊", "Tai / Peace", "harmony, openness, and smooth exchange", "Use the easy current to build goodwill and finish practical work.", "Do not become careless just because things feel calm."},
	{12, "䷋", "Pi / Standstill", "blocked flow and necessary distance", "Conserve energy and avoid pushing against a closed door.", "Bitterness will keep the blockage alive longer."},
	{13, "䷌", "Tong Ren / Fellowship", "shared purpose and honest connection", "Find common ground; a sincere conversation can align efforts.", "Agreement is not the same as true understanding."},
	{14, "䷍", "Da You / Great Possession", "abundance, responsibility, and visibility", "Use what you have generously and wisely.", "Pride can turn blessing into burden."},
	{15, "䷎", "Qian / Modesty", "humility that creates trust", "Let the work speak. A grounded attitude attracts support.", "False modesty is still performance."},
	{16, "䷏", "Yu / Enthusiasm", "motivation, rhythm, and momentum", "Give your energy a clear beat: plan, act, celebrate, repeat.", "Excitement without structure fades quickly."},
	{17, "䷐", "Sui / Following", "adaptation and responsive movement", "Follow the living signal rather than yesterday's plan.", "Do not follow merely to avoid choosing."},
	{18, "䷑", "Gu / Work on What Has Been Spoiled", "repair, correction, and inherited messes", "Fix one neglected thing; renewal begins with honest maintenance.", "Blame will not repair what attention can."},
	{19, "䷒", "Lin / Approach", "opportunity drawing near", "Step closer with warmth and readiness.", "A good opening still asks for follow-through."},
	{20, "䷓", "Guan / Contemplation", "perspective and observation", "Pause above the noise and look for the pattern.", "Watching too long can become avoidance."},
	{21, "䷔", "Shi He / Biting Through", "decisive action and cutting through confusion", "Name the issue plainly and remove the obstacle.", "Harshness may create a second problem."},
	{22, "䷕", "Bi / Grace", "beauty, presentation, and refinement", "Polish the form, but keep the substance honest.", "Decoration cannot replace depth."},
	{23, "䷖", "Bo / Splitting Apart", "letting unstable things fall away", "Protect your center and release what is already crumbling.", "Do not build on a weakening foundation."},
	{24, "䷗", "Fu / Return", "renewal and the first step back", "Return to a good habit, a true value, or a simple beginning.", "A fresh start still needs repetition."},
	{25, "䷘", "Wu Wang / Innocence", "naturalness and unforced truth", "Act simply and sincerely; let motives stay clean.", "Scheming against the moment will backfire."},
	{26, "䷙", "Da Chu / Great Taming", "stored power and disciplined restraint", "Hold your strength until it can serve a larger aim.", "Suppressed energy needs purpose, not denial."},
	{27, "䷚", "Yi / Nourishment", "what you feed and what feeds you", "Choose inputs carefully: food, words, media, and company.", "You cannot thrive on what drains you."},
	{28, "䷛", "Da Guo / Great Exceeding", "pressure, overload, and bold adjustment", "Rebalance before the beam bends too far.", "Carrying everything alone is not courage."},
	{29, "䷜", "Kan / The Abysmal", "repeated challenge and inner steadiness", "Move through difficulty step by step; keep your heart awake.", "Panic deepens the water."},
	{30, "䷝", "Li / The Clinging", "clarity, warmth, and dependence on what is bright", "Stay close to what illuminates your next right action.", "Attachment to appearances can burn."},
	{31, "䷞", "Xian / Influence", "attraction and subtle response", "A gentle gesture may move more than a demand.", "Do not confuse influence with control."},
	{32, "䷟", "Heng / Duration", "commitment and sustainable rhythm", "Keep the promise in a manageable way.", "Rigidity can break what consistency would preserve."},
	{33, "䷠", "Dun / Retreat", "strategic withdrawal", "Step back to protect your strength and perspective.", "Retreat is wise only when it remains conscious."},
	{34, "䷡", "Da Zhuang / Great Power", "strength seeking right use", "Use power proportionally; act with integrity.", "Force used for ego invites resistance."},
	{35, "䷢", "Jin / Progress", "visibility and gradual advancement", "Take the next visible step and let your work be seen.", "Progress can tempt you to rush the lesson."},
	{36, "䷣", "Ming Yi / Darkening of the Light", "protecting inner brightness", "Keep your wisdom quiet if the room cannot hold it yet.", "Hiding forever dims the light you are protecting."},
	{37, "䷤", "Jia Ren / The Family", "roles, care, and healthy order", "Tend the small circle: home, team, or daily routine.", "Unspoken expectations can become resentment."},
	{38, "䷥", "Kui / Opposition", "difference, contrast, and careful alignment", "Respect differences while seeking one practical point of agreement.", "Making someone wrong may close the only bridge."},
	{39, "䷦", "Jian / Obstruction", "delays that redirect effort", "Go around, ask counsel, or wait for safer footing.", "Stubbornness turns an obstacle into a wall."},
	{40, "䷧", "Xie / Deliverance", "release after tension", "Loosen the knot; forgive, simplify, or finish what is overdue.", "Relief should not become carelessness."},
	{41, "䷨", "Sun / Decrease", "simplification and willing sacrifice", "Remove one excess so what matters can breathe.", "Cutting the essential is not simplicity."},
	{42, "䷩", "Yi / Increase", "growth through generosity", "Invest energy where it multiplies benefit for more than yourself.", "Expansion without grounding scatters energy."},
	{43, "䷪", "Guai / Breakthrough", "truth spoken at the right moment", "Be clear, courageous, and measured.", "A breakthrough delivered with anger can become a break."},
	{44, "䷫", "Gou / Coming to Meet", "unexpected encounter and strong attraction", "Notice what arrives suddenly; engage with discernment.", "Not every powerful invitation is healthy."},
	{45, "䷬", "Cui / Gathering Together", "assembly, focus, and shared resources", "Bring people or materials together around one clear purpose.", "A crowd without direction disperses energy."},
	{46, "䷭", "Sheng / Pushing Upward", "steady ascent and patient ambition", "Rise by small, honest increments.", "Skipping steps weakens the climb."},
	{47, "䷮", "Kun / Oppression", "constraint and inner resource", "When options are limited, protect your spirit and choose the possible.", "Complaining spends energy you may need."},
	{48, "䷯", "Jing / The Well", "shared source and replenishment", "Return to what reliably nourishes you and others.", "A good source still needs maintenance."},
	{49, "䷰", "Ge / Revolution", "necessary change and renewal", "Change what has truly outlived its time.", "Rebellion without timing creates needless chaos."},
	{50, "䷱", "Ding / The Cauldron", "transformation, culture, and offering", "Refine raw material into something useful and nourishing.", "Do not serve from an empty vessel."},
	{51, "䷲", "Zhen / The Arousing", "shock, awakening, and movement", "Let surprise wake you up without throwing you off center.", "Reacting too fast may amplify the thunder."},
	{52, "䷳", "Gen / Keeping Still", "stillness and boundary", "Stop long enough to know what is yours to do.", "Stillness becomes stagnation when fear is driving it."},
	{53, "䷴", "Jian / Gradual Development", "slow growth and proper sequence", "Let progress mature naturally; trust the sequence.", "Impatience may uproot what is growing."},
	{54, "䷵", "Gui Mei / The Marrying Maiden", "imperfect position and adaptation", "Work skillfully with the role you have, not the role you wish you had.", "Compromise should not cost self-respect."},
	{55, "䷶", "Feng / Abundance", "fullness, peak energy, and illumination", "Use the bright moment well; make decisions while things are visible.", "Abundance fades if it is not directed."},
	{56, "䷷", "Lu / The Wanderer", "travel, transition, and humility in unfamiliar places", "Travel light and be courteous in unknown territory.", "Temporary places require extra care."},
	{57, "䷸", "Xun / The Gentle Wind", "gradual influence and subtle penetration", "Repeat the gentle message; persistence enters where force cannot.", "Being too indirect may hide your truth."},
	{58, "䷹", "Dui / Joy", "openness, pleasure, and exchange", "Share encouragement and let joy restore momentum.", "Pleasure without sincerity becomes hollow."},
	{59, "䷺", "Huan / Dispersion", "dissolving tension and widening the field", "Break up stuck energy: walk, talk, clean, or forgive.", "Scattering focus can dissolve progress too."},
	{60, "䷻", "Jie / Limitation", "healthy boundaries and measured limits", "Set a kind limit; structure will protect what matters.", "Rules without humanity become confinement."},
	{61, "䷼", "Zhong Fu / Inner Truth", "sincerity and trustworthy perception", "Listen beneath the surface and speak from the center.", "Self-deception is the first lie to correct."},
	{62, "䷽", "Xiao Guo / Small Exceeding", "attention to small things", "Do less, do it carefully, and keep expectations modest.", "Grand gestures may miss the practical need."},
	{63, "䷾", "Ji Ji / After Completion", "completion requiring vigilance", "Enjoy the milestone, then maintain what made it possible.", "The moment after success is easy to neglect."},
	{64, "䷿", "Wei Ji / Before Completion", "unfinished transition and careful finishing", "You are near the crossing; check the details before the final step.", "Almost done is not done."},
}

var ichingDailyLessons = []string{
	"Strong beginnings work best when ambition is guided by patience and care.",
	"The quiet strength to receive, support, and wait can move life farther than force.",
	"When a new beginning feels tangled, start with one small orderable step.",
	"A sincere question is often wiser than a confident answer given too soon.",
	"Waiting becomes useful when you spend the pause preparing yourself well.",
	"In conflict, protect what matters without letting pride choose your words.",
	"Discipline turns scattered effort into a path others can trust and follow.",
	"The right companions strengthen your direction without asking you to disappear.",
	"Small restraints and careful habits often prevent the larger problem from forming.",
	"Move through sensitive situations with respect, because tact protects courage.",
	"When life feels peaceful, use the calm to build something that can last.",
	"If the way is blocked, conserve your energy and keep your character clear.",
	"Shared purpose grows when people are met with honesty rather than performance.",
	"Abundance becomes meaningful when it is carried with responsibility and generosity.",
	"Modesty is not hiding your worth, but letting your actions remain well-grounded.",
	"Enthusiasm becomes power when it is given rhythm, structure, and follow-through.",
	"Adapt to what is alive now instead of obeying a plan that has gone stale.",
	"Repair begins when you stop blaming the damage and give attention to what can be restored.",
	"When an opening appears, step toward it warmly and be ready to do your part.",
	"A wider view can reveal the pattern that urgency keeps hidden.",
	"Some obstacles must be named clearly before they can be removed cleanly.",
	"Grace is strongest when beauty and presentation serve something true underneath.",
	"Let unstable things fall away before they teach you through collapse.",
	"Returning to one good habit can become the doorway back to yourself.",
	"Simple motives protect simple actions from becoming complicated later.",
	"Stored strength becomes wisdom when it waits for the right purpose.",
	"Be careful what you consume, because your days are shaped by what you repeatedly take in.",
	"When the load is too heavy, courage may mean redistributing it before it breaks you.",
	"Repeated difficulty asks for steady steps, not dramatic reactions.",
	"Stay close to what clarifies your next action and warms your spirit.",
	"Gentle influence often reaches places that pressure cannot enter.",
	"What lasts is built by promises small enough to keep consistently.",
	"Stepping back at the right time can preserve the strength needed for a better return.",
	"Real power is measured by how carefully it chooses where not to push.",
	"Progress is healthiest when each visible step remains honest and earned.",
	"When the room is not ready for your light, protect it without abandoning it.",
	"Tending the small circle around you is often the beginning of larger order.",
	"Difference becomes useful when you seek the bridge before proving the distance.",
	"An obstruction may be asking you to change route rather than increase force.",
	"Release arrives when you loosen the knot instead of rehearsing how it was tied.",
	"Removing one excess can give what truly matters enough room to breathe.",
	"Growth is most generous when it increases the well-being of more than yourself.",
	"A breakthrough needs clarity and restraint so truth does not become harm.",
	"A powerful invitation deserves discernment before it receives your trust.",
	"Gather your people, tools, and attention around one purpose before you move.",
	"Lasting ascent is made of small honest steps repeated without vanity.",
	"When options are narrow, dignity is preserved by choosing the possible with care.",
	"Return often to the source that nourishes you, and help keep it clean.",
	"Change is strongest when it answers a real season rather than a restless mood.",
	"Transformation asks you to refine what is raw into something that can nourish others.",
	"A sudden shock can wake you without needing to rule your next move.",
	"Stillness helps you know what is yours to carry and what should be left alone.",
	"Gradual growth honors timing, sequence, and the patience of real maturity.",
	"Work skillfully with the role you have while keeping your self-respect intact.",
	"Fullness is a moment to use well, not a reason to become careless.",
	"In unfamiliar places, travel lightly and let humility keep you safe.",
	"Gentle repetition can shape a life more deeply than one forceful effort.",
	"Joy restores energy when it remains sincere, shared, and grounded.",
	"Stuck energy often clears when you make space, simplify, and let something move.",
	"A kind limit protects your attention, your relationships, and your peace.",
	"Inner truth becomes trustworthy when you listen before you speak.",
	"Small careful actions matter most when grand gestures would only disturb the moment.",
	"Completion still needs attention, because what is finished must now be maintained.",
	"Before the final step, check the details so the crossing can be clean.",
}

func ichingDailyLesson(f ichingFortune) string {
	idx := f.Number - 1
	if idx >= 0 && idx < len(ichingDailyLessons) {
		return ichingDailyLessons[idx]
	}
	return f.Advice
}

func (m Model) enterFortuneView() (tea.Model, tea.Cmd) {
	m.mode = modeFortune
	m.fortuneScroll = 0
	m.status = "Today’s reading"
	return m, nil
}

func (m Model) updateFortuneMode(key string) (tea.Model, tea.Cmd) {
	if m.processScrollKey(key, m.fortuneMaxScroll(), &m.fortuneScroll) {
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Reading closed"
		return m, nil
	case ":":
		return m.startCommand()
	case m.cfg.Keys.Up, "up":
		if m.fortuneScroll > 0 {
			m.fortuneScroll--
		}
	case m.cfg.Keys.Down, "down":
		m.fortuneScroll = clampInt(m.fortuneScroll+1, 0, m.fortuneMaxScroll())
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) fortuneMaxScroll() int {
	bodyMax := m.fortuneBodyMaxLines()
	if bodyMax <= 0 {
		return 0
	}
	lines := m.fortuneLines()
	if len(lines) <= bodyMax {
		return 0
	}
	return len(lines) - bodyMax
}

func (m Model) fortuneBodyMaxLines() int {
	if m.height <= 0 {
		return 0
	}
	bodyMax := m.height - 1 - 2 - countLines(m.fortuneFooter())
	if bodyMax < 1 {
		bodyMax = 1
	}
	return bodyMax
}

func (m Model) renderFortuneView() string {
	footer := m.fortuneFooter()
	lines := m.fortuneLines()
	if m.height > 0 {
		bodyMax := m.fortuneBodyMaxLines()
		maxScroll := len(lines) - bodyMax
		if maxScroll < 0 {
			maxScroll = 0
		}
		scroll := clampInt(m.fortuneScroll, 0, maxScroll)
		end := scroll + bodyMax
		if end > len(lines) {
			end = len(lines)
		}
		lines = lines[scroll:end]
	}
	return m.panel("bada ∙ I Ching Reading", strings.Join(lines, "\n")) + "\n" + footer
}

func (m Model) fortuneFooter() string {
	return m.hintBar([]keyHint{{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "scroll"}, {":", "command"}, {m.cfg.Keys.Cancel, "close"}})
}

func (m Model) fortuneLines() []string {
	f := dailyIChingFortune(time.Now())
	inner := m.panelInnerWidth()
	wrapW := inner - 4
	if wrapW < 24 {
		wrapW = inner
	}
	// The hexagram symbol + name anchors the reading; the rest is just the lesson
	// and its follow-on sentences, with no "Theme/Advice/Caution" labels.
	lines := []string{
		"  " + m.styles.Accent.Bold(true).Render(f.Symbol+"   "+shortHexagramName(f.Name)),
		"",
	}
	para := func(body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		lines = append(lines, indentWrapped(body, wrapW, "  ")...)
		lines = append(lines, "")
	}
	para(ichingDailyLesson(f))
	para(f.Advice)
	para(f.Caution)
	return lines
}

// shortHexagramName returns the readable English name of a hexagram, e.g.
// "Bi / Grace" → "Grace", "Qian / The Creative" → "The Creative".
func shortHexagramName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return strings.TrimSpace(name[i+1:])
	}
	return strings.TrimSpace(name)
}

func dailyIChingFortune(t time.Time) ichingFortune {
	key := t.Format("2006-01-02")
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum32()) % len(ichingFortunes)
	return ichingFortunes[idx]
}

func indentWrapped(text string, width int, indent string) []string {
	wrapped := wrapText(text, width)
	if wrapped == "" {
		return []string{indent}
	}
	raw := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, indent+line)
	}
	return out
}
