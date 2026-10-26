package sports

import "time"

// SportsTeam represents one side in a scheduled or in-progress game.
type SportsTeam struct {
	Seed             int    `json:"seed,omitempty"`
	StandingSummary  string `json:"standingSummary,omitempty"`
	Color            string `json:"color,omitempty"`
	AlternateColor   string `json:"alternateColor,omitempty"`
	Rank             int    `json:"rank,omitempty"`
	Record           string `json:"record,omitempty"`
	ConferenceRecord string `json:"conferenceRecord,omitempty"`
	ShootoutScore    *int   `json:"shootoutScore,omitempty"`
	ID               string `json:"id"`
	Name             string `json:"name"`
	Location         string `json:"location,omitempty"`
	Nickname         string `json:"nickname,omitempty"`
	Abbreviation     string `json:"abbreviation,omitempty"`
	LogoURL          string `json:"logoUrl,omitempty"`
	Score            string `json:"score,omitempty"`
	Winner           bool   `json:"winner,omitempty"`
}

// SportsGameStatus is the coarse lifecycle state of a game.
type SportsGameStatus string

const (
	SportsGameScheduled SportsGameStatus = "scheduled"
	SportsGameLive      SportsGameStatus = "live"
	SportsGameFinal     SportsGameStatus = "final"
)

// SportsGame represents a single scheduled, live, or completed game.
type SportsGame struct {
	Combat          *SportsCombatContext `json:"combat,omitempty"`
	CourtName       string               `json:"courtName,omitempty"`
	BestOf          int                  `json:"bestOf,omitempty"`
	NeutralSite     *bool                `json:"neutralSite,omitempty"`
	ProviderEventID string               `json:"providerEventId,omitempty"`
	ParentEventID   string               `json:"parentEventId,omitempty"`

	EventContext      string                   `json:"eventContext,omitempty"`
	EndTime           time.Time                `json:"endTime,omitempty"`
	FootballSituation *SportsFootballSituation `json:"footballSituation,omitempty"`
	LiveSituation     *SportsMLBSituation      `json:"liveSituation,omitempty"`
	Detail            *SportsGameDetail        `json:"detail,omitempty"`
	ID                string                   `json:"id"`
	Title             string                   `json:"title,omitempty"`
	EventKind         string                   `json:"eventKind,omitempty"`
	League            string                   `json:"league"`
	Sport             string                   `json:"sport"`
	StartTime         time.Time                `json:"startTime"`
	Status            SportsGameStatus         `json:"status"`
	StatusDetail      string                   `json:"statusDetail,omitempty"`
	Period            string                   `json:"period,omitempty"`
	Clock             string                   `json:"clock,omitempty"`
	HomeTeam          SportsTeam               `json:"homeTeam"`
	AwayTeam          SportsTeam               `json:"awayTeam"`
	Broadcasts        []string                 `json:"broadcasts,omitempty"`
	VenueName         string                   `json:"venue,omitempty"`
	Participants      []SportsParticipant      `json:"participants,omitempty"`
}

// SportsLeague identifies a supported league/competition.
type SportsLeague struct {
	Provider             string   `json:"provider,omitempty"`
	ProviderSport        string   `json:"providerSport,omitempty"`
	ProviderLeague       string   `json:"providerLeague,omitempty"`
	ApplicationSport     string   `json:"applicationSport,omitempty"`
	College              bool     `json:"college"`
	Adapter              string   `json:"adapter,omitempty"`
	ImplementationStatus string   `json:"implementationStatus,omitempty"`
	Capabilities         []string `json:"capabilities"`
	Aliases              []string `json:"aliases,omitempty"`
	CoverageNote         string   `json:"coverageNote,omitempty"`

	ID            string `json:"id"`
	Name          string `json:"name"`
	Sport         string `json:"sport"`
	Category      string `json:"category"`
	EventKind     string `json:"eventKind"`
	SupportsTeams bool   `json:"supportsTeams"`
	Enabled       bool   `json:"enabled"`
}

// SportsParticipant and SportsEvent are the generalized hub contract. The
// legacy game shape above remains intact for scoreboard clients.
type SportsParticipant struct {
	Number       string                `json:"number,omitempty"`
	Team         string                `json:"team,omitempty"`
	Result       string                `json:"result,omitempty"`
	Statistics   []SportsRaceStatistic `json:"statistics,omitempty"`
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Abbreviation string                `json:"abbreviation,omitempty"`
	LogoURL      string                `json:"logoUrl,omitempty"`
	Score        string                `json:"score,omitempty"`
	Position     int                   `json:"position,omitempty"`
	Winner       bool                  `json:"winner,omitempty"`
}

type SportsRaceStatistic struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// SportsCircuit is licensed community reference geometry, never live tracking.
type SportsCircuit struct {
	ID                    string       `json:"id"`
	Name                  string       `json:"name"`
	Location              string       `json:"location"`
	ReferenceLengthMeters int          `json:"referenceLengthMeters"`
	Coordinates           [][2]float64 `json:"coordinates"`
	SourceURL             string       `json:"sourceUrl"`
	Attribution           string       `json:"attribution"`
}

type SportsEvent struct {
	Circuit         *SportsCircuit      `json:"circuit,omitempty"`
	ProviderEventID string              `json:"providerEventId,omitempty"`
	SessionID       string              `json:"sessionId,omitempty"`
	SessionType     string              `json:"sessionType,omitempty"`
	UpdatedAt       time.Time           `json:"updatedAt,omitempty"`
	Stale           bool                `json:"stale,omitempty"`
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	League          string              `json:"league"`
	Sport           string              `json:"sport"`
	EventKind       string              `json:"eventKind"`
	StartTime       time.Time           `json:"startTime"`
	Status          SportsGameStatus    `json:"status"`
	StatusDetail    string              `json:"statusDetail,omitempty"`
	Participants    []SportsParticipant `json:"participants"`
	SubEvents       []SportsEvent       `json:"subEvents,omitempty"`
	Broadcasts      []string            `json:"broadcasts,omitempty"`
	VenueName       string              `json:"venue,omitempty"`
	Featured        bool                `json:"featured,omitempty"`
}

// SportsStatus reports the health of the sports scoreboard service.
// SportsReportedQuality is an explicit label-derived quality, never a probe.
type SportsReportedQuality struct {
	ResolutionHeight int    `json:"resolutionHeight,omitempty"`
	BitrateBps       int64  `json:"bitrateBps,omitempty"`
	Origin           string `json:"origin"`
}

// SportsStreamMatch is one candidate Live TV channel for watching a game,
// found by heuristic matching. Best-effort, not authoritative.
type SportsStreamMatch struct {
	ReportedQuality *SportsReportedQuality `json:"reportedQuality,omitempty"`
	ChannelID       string                 `json:"channelId"`
	ChannelName     string                 `json:"channelName"`
	ChannelURL      string                 `json:"channelUrl"`
	ChannelLogo     string                 `json:"channelLogo,omitempty"`
	ChannelTvgID    string                 `json:"channelTvgId,omitempty"`
	SourceID        string                 `json:"sourceId,omitempty"`
	SourceName      string                 `json:"sourceName,omitempty"`
	ProgramTitle    string                 `json:"programTitle,omitempty"`
	MatchedOn       string                 `json:"matchedOn"`
	Broadcast       string                 `json:"broadcast,omitempty"`
	MatchedTeam     string                 `json:"matchedTeam,omitempty"`
	Confidence      float64                `json:"confidence"`
	ConfidenceTier  string                 `json:"confidenceTier"`
	MatchReason     string                 `json:"matchReason"`
	MatchedTerms    []string               `json:"matchedTerms,omitempty"`
	LifecycleState  string                 `json:"lifecycleState,omitempty"`
}

// SportsStreamGroup collapses equivalent playlist entries while preserving
// every distinct feed as an explicit alternative.
type SportsStreamGroup struct {
	GroupKey       string              `json:"groupKey"`
	DisplayName    string              `json:"displayName"`
	Confidence     float64             `json:"confidence"`
	ConfidenceTier string              `json:"confidenceTier"`
	MatchReason    string              `json:"matchReason"`
	FeedCount      int                 `json:"feedCount"`
	Primary        SportsStreamMatch   `json:"primary"`
	Alternatives   []SportsStreamMatch `json:"alternatives"`
}

// SportsChannelCandidate is a managed team-to-channel link candidate.
type SportsChannelCandidate struct {
	ChannelID      string   `json:"channelId"`
	ChannelName    string   `json:"channelName"`
	ChannelURL     string   `json:"channelUrl"`
	ChannelLogo    string   `json:"channelLogo,omitempty"`
	ChannelTvgID   string   `json:"channelTvgId,omitempty"`
	SourceID       string   `json:"sourceId,omitempty"`
	SourceName     string   `json:"sourceName,omitempty"`
	Group          string   `json:"group,omitempty"`
	Confidence     float64  `json:"confidence"`
	ConfidenceTier string   `json:"confidenceTier"`
	MatchReason    string   `json:"matchReason"`
	MatchedTerms   []string `json:"matchedTerms,omitempty"`
}

type SportsStatus struct {
	DateQueries bool       `json:"dateQueries"`
	Enabled     bool       `json:"enabled"`
	LastRefresh *time.Time `json:"lastRefresh,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	GameCount   int        `json:"gameCount"`
	Refreshing  bool       `json:"refreshing"`
}

// SportsTeamRecord is a persisted team identity keyed on league+provider team ID
// rather than name so it survives the team being renamed/rebranded upstream.
type SportsTeamRecord struct {
	ID           string    `json:"id"` // "{league}:{providerTeamId}"
	League       string    `json:"league"`
	ProviderID   string    `json:"providerTeamId"`
	Name         string    `json:"name"`
	Location     string    `json:"location,omitempty"`
	Nickname     string    `json:"nickname,omitempty"`
	Abbreviation string    `json:"abbreviation,omitempty"`
	LogoURL      string    `json:"logoUrl,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// SportsTeamChannelLink is one Live TV channel permanently linked to a team.
// ChannelID is the current playlist channel resolved at read time; the stored
// tvg id/name snapshot is what makes that resolution self-healing. ChannelURL
// is deliberately absent: provider stream URLs never leave the server.
type SportsTeamChannelLink struct {
	ID             string    `json:"id"`
	TeamID         string    `json:"teamId"`
	Slot           string    `json:"slot"` // "primary" or "backup"
	Position       int       `json:"position"`
	ChannelID      string    `json:"channelId,omitempty"`
	ChannelRef     string    `json:"channelRef,omitempty"`
	ChannelTvgID   string    `json:"channelTvgId,omitempty"`
	ChannelName    string    `json:"channelName"`
	ChannelLogo    string    `json:"channelLogo,omitempty"`
	SourceID       string    `json:"sourceId,omitempty"`
	SourceName     string    `json:"sourceName,omitempty"`
	AutoLinked     bool      `json:"autoLinked"`
	LinkConfidence float64   `json:"linkConfidence,omitempty"`
	MatchReason    string    `json:"matchReason,omitempty"`
	LastVerifiedAt time.Time `json:"lastVerifiedAt,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Resolved       bool      `json:"resolved"`
}

// SportsTeamWithLinks is a team plus its current links for the Team Channels
// administration page.
type SportsTeamWithLinks struct {
	SportsTeamRecord
	Links []SportsTeamChannelLink `json:"links"`
}

// SportsAutoLinkTeamResult explains, per team, exactly why an automatic link
// was or was not proposed. It is a preview: applying is a separate action.
type SportsAutoLinkTeamResult struct {
	Team               SportsTeamRecord        `json:"team"`
	Candidate          *SportsChannelCandidate `json:"candidate,omitempty"`
	RunnerUpConfidence float64                 `json:"runnerUpConfidence,omitempty"`
	ConfidenceGap      float64                 `json:"confidenceGap,omitempty"`
	Eligible           bool                    `json:"eligible"`
	SkipReason         string                  `json:"skipReason,omitempty"`
}

type SportsAutoLinkPreview struct {
	League        string                     `json:"league"`
	Message       string                     `json:"message,omitempty"`
	EligibleCount int                        `json:"eligibleCount"`
	SkippedCount  int                        `json:"skippedCount"`
	Results       []SportsAutoLinkTeamResult `json:"results"`
}

// SportsFootballSituation keeps optional timeout counts distinct from zero.
type SportsFootballSituation struct {
	Kind          string `json:"kind"`
	Possession    string `json:"possession"`
	DownDistance  string `json:"downDistance"`
	FieldPosition string `json:"fieldPosition"`
	AwayTimeouts  *int   `json:"awayTimeouts,omitempty"`
	HomeTimeouts  *int   `json:"homeTimeouts,omitempty"`
}

// SportsCombatContext preserves the provider card order without guessing
// main/prelim sections.
type SportsCombatContext struct {
	CardName        string             `json:"cardName,omitempty"`
	Division        string             `json:"division,omitempty"`
	ScheduledRounds int                `json:"scheduledRounds,omitempty"`
	Bouts           []SportsCombatBout `json:"bouts,omitempty"`
}

type SportsCombatBout struct {
	ID       string           `json:"id"`
	Away     SportsTeam       `json:"away"`
	Home     SportsTeam       `json:"home"`
	Division string           `json:"division,omitempty"`
	Status   SportsGameStatus `json:"status"`
	Period   string           `json:"period,omitempty"`
	Clock    string           `json:"clock,omitempty"`
}

// SportsGameDetail is additive to the game response. Empty data never implies a
// zero statistic; capability flags describe verified available panels.
type SportsGameDetail struct {
	CricketScorecards []SportsCricketScorecard    `json:"cricketScorecards,omitempty"`
	BasketballShots   []SportsBasketballShot      `json:"basketballShots,omitempty"`
	WinProbability    []SportsWinProbabilityPoint `json:"winProbability,omitempty"`
	FighterProfiles   []SportsPlayerGameStats     `json:"fighterProfiles,omitempty"`
	Shots             []SportsHockeyShot          `json:"shots,omitempty"`
	Leaderboard       []SportsGolfEntry           `json:"leaderboard,omitempty"`
	Innings           []SportsCricketInnings      `json:"innings,omitempty"`
	CoverageNote      string                      `json:"coverageNote,omitempty"`
	Pregame           *SportsPregame              `json:"pregame,omitempty"`
	PlayerStats       []SportsPlayerGameStats     `json:"playerStats,omitempty"`
	TournamentContext string                      `json:"tournamentContext,omitempty"`
	ScoreHistory      *SportsScoreHistory         `json:"scoreHistory,omitempty"`
	FootballDrives    []SportsFootballDrive       `json:"footballDrives,omitempty"`
	Lineups           []SportsLineup              `json:"lineups,omitempty"`
	Standings         []SportsStandingGroup       `json:"standings,omitempty"`
	Leaders           []SportsDetailLeader        `json:"leaders,omitempty"`
	Sport             *SportsMLBSituation         `json:"sport,omitempty"`
	Source            string                      `json:"source"`
	UpdatedAt         time.Time                   `json:"updatedAt"`
	Stale             bool                        `json:"stale"`
	Periods           []SportsPeriodScore         `json:"periods"`
	Plays             []SportsDetailPlay          `json:"plays"`
	Comparisons       []SportsComparison          `json:"comparisons"`
	Capabilities      SportsDetailCapabilities    `json:"capabilities"`
}

type SportsPeriodScore struct {
	AwayTiebreak *int   `json:"awayTiebreak,omitempty"`
	HomeTiebreak *int   `json:"homeTiebreak,omitempty"`
	AwayWinner   *bool  `json:"awayWinner,omitempty"`
	HomeWinner   *bool  `json:"homeWinner,omitempty"`
	Label        string `json:"label"`
	Away         string `json:"away"`
	Home         string `json:"home"`
}

type SportsPlayParticipant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SportsPlayerGameStats struct {
	Jersey      string                  `json:"jersey,omitempty"`
	HeadshotURL string                  `json:"headshotUrl,omitempty"`
	ID          string                  `json:"id"`
	TeamID      string                  `json:"teamId"`
	Name        string                  `json:"name"`
	Category    string                  `json:"category"`
	Stats       []SportsPlayerStatistic `json:"stats"`
}

type SportsPlayerStatistic struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type SportsDetailPlay struct {
	TeamID       string                  `json:"teamId,omitempty"`
	Participants []SportsPlayParticipant `json:"participants,omitempty"`
	Shootout     bool                    `json:"shootout,omitempty"`
	Clock        string                  `json:"clock,omitempty"`
	ID           string                  `json:"id"`
	PeriodLabel  string                  `json:"periodLabel"`
	Title        string                  `json:"title"`
	Description  string                  `json:"description,omitempty"`
	Scoring      bool                    `json:"scoring"`
}

type SportsComparison struct {
	Label string `json:"label"`
	Away  string `json:"away"`
	Home  string `json:"home"`
}

type SportsDetailCapabilities struct {
	Plays          bool `json:"plays"`
	Stats          bool `json:"stats"`
	WinProbability bool `json:"winProbability"`
}

// Pointer counts preserve a real zero without inventing absent live values.
type SportsMLBSituation struct {
	BatterID          string `json:"batterId,omitempty"`
	PitcherID         string `json:"pitcherId,omitempty"`
	PitcherPitchCount *int   `json:"pitcherPitchCount,omitempty"`
	Bases             string `json:"bases,omitempty"`
	Kind              string `json:"kind"`
	Inning            string `json:"inning"`
	Balls             *int   `json:"balls,omitempty"`
	Strikes           *int   `json:"strikes,omitempty"`
	Outs              *int   `json:"outs,omitempty"`
	Pitcher           string `json:"pitcher,omitempty"`
	Batter            string `json:"batter,omitempty"`
}

type SportsDetailLeader struct {
	AthleteID        string `json:"athleteId,omitempty"`
	HeadshotURL      string `json:"headshotUrl,omitempty"`
	Label            string `json:"label"`
	Name             string `json:"name"`
	TeamAbbreviation string `json:"teamAbbreviation"`
	Value            string `json:"value"`
}

// Match lineups and table snapshots have distinct scopes; tables are not
// as-of-game results.
type SportsLineup struct {
	TeamID    string               `json:"teamId"`
	Formation string               `json:"formation,omitempty"`
	Players   []SportsLineupPlayer `json:"players"`
}

type SportsLineupPlayer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Number    string `json:"number,omitempty"`
	Position  string `json:"position,omitempty"`
	Starter   bool   `json:"starter"`
	SubbedIn  bool   `json:"subbedIn,omitempty"`
	SubbedOut bool   `json:"subbedOut,omitempty"`
}

type SportsStandingGroup struct {
	Title string              `json:"title"`
	Rows  []SportsStandingRow `json:"rows"`
}

type SportsStandingRow struct {
	TeamID           string `json:"teamId"`
	Name             string `json:"name"`
	Rank             string `json:"rank,omitempty"`
	Played           string `json:"played,omitempty"`
	Points           string `json:"points,omitempty"`
	GoalDifference   string `json:"goalDifference,omitempty"`
	Record           string `json:"record,omitempty"`
	ConferenceRecord string `json:"conferenceRecord,omitempty"`
}

// Coordinates orient the away end at 0 and the home end at 100 for consistent
// comparison.
type SportsFootballPlay struct {
	TypeID           string   `json:"typeId,omitempty"`
	Wallclock        string   `json:"wallclock,omitempty"`
	Down             *int     `json:"down,omitempty"`
	Distance         *float64 `json:"distance,omitempty"`
	Yards            *float64 `json:"yards,omitempty"`
	PossessionTeamID string   `json:"possessionTeamId,omitempty"`
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Description      string   `json:"description"`
	Clock            string   `json:"clock"`
	Period           string   `json:"period"`
	Start            *float64 `json:"start,omitempty"`
	End              *float64 `json:"end,omitempty"`
	Scoring          bool     `json:"scoring"`
}

type SportsFootballDrive struct {
	PlayCount   *int                 `json:"playCount,omitempty"`
	Yards       *float64             `json:"yards,omitempty"`
	Elapsed     string               `json:"elapsed,omitempty"`
	StartKnown  *bool                `json:"startKnown,omitempty"`
	EndKnown    *bool                `json:"endKnown,omitempty"`
	Plays       []SportsFootballPlay `json:"plays,omitempty"`
	ID          string               `json:"id"`
	TeamID      string               `json:"teamId"`
	Start       float64              `json:"start"`
	End         float64              `json:"end"`
	StartLabel  string               `json:"startLabel"`
	EndLabel    string               `json:"endLabel"`
	Period      string               `json:"period"`
	Result      string               `json:"result"`
	Description string               `json:"description"`
	Current     bool                 `json:"current"`
}

// SportsScoreHistory is a validated complete final snapshot, replaced on
// refresh.
type SportsScoreHistory struct {
	EventID    string             `json:"eventId"`
	AwayTeamID string             `json:"awayTeamId"`
	HomeTeamID string             `json:"homeTeamId"`
	Complete   bool               `json:"complete"`
	Points     []SportsScorePoint `json:"points"`
}

type SportsScorePoint struct {
	ID          string  `json:"id"`
	Sequence    string  `json:"sequence"`
	Period      int     `json:"period"`
	PeriodLabel string  `json:"periodLabel"`
	Clock       string  `json:"clock"`
	Away        float64 `json:"away"`
	Home        float64 `json:"home"`
}

// Golf preserves the provider order without claiming that order is a tied rank.
type SportsGolfEntry struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Score  string            `json:"score,omitempty"`
	Rounds []SportsGolfRound `json:"rounds,omitempty"`
}

type SportsGolfRound struct {
	Round   int              `json:"round"`
	Strokes string           `json:"strokes,omitempty"`
	ToPar   string           `json:"toPar,omitempty"`
	Holes   []SportsGolfHole `json:"holes,omitempty"`
}

type SportsGolfHole struct {
	Hole    int    `json:"hole"`
	Strokes string `json:"strokes"`
	ToPar   string `json:"toPar,omitempty"`
}

type SportsCricketInnings struct {
	Score       string `json:"score,omitempty"`
	Target      *int   `json:"target,omitempty"`
	Batting     *bool  `json:"batting,omitempty"`
	TeamID      string `json:"teamId"`
	Number      int    `json:"number"`
	Runs        *int   `json:"runs,omitempty"`
	Wickets     *int   `json:"wickets,omitempty"`
	Overs       string `json:"overs,omitempty"`
	Description string `json:"description,omitempty"`
}

// Provider rink coordinates in feet, centered at (0,0). No attack-direction
// inference.
type SportsHockeyShot struct {
	ID          string  `json:"id"`
	TeamID      string  `json:"teamId"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Kind        string  `json:"kind"`
	Period      string  `json:"period"`
	Clock       string  `json:"clock"`
	Description string  `json:"description"`
}

// Basketball coordinates are feet across the court (x) and from the attacking
// rim (y); a normalized attacking basket, never broadcast direction.
type SportsBasketballShot struct {
	Jersey      string  `json:"jersey,omitempty"`
	ID          string  `json:"id"`
	TeamID      string  `json:"teamId"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Made        bool    `json:"made"`
	Points      int     `json:"points"`
	Period      int     `json:"period"`
	PeriodLabel string  `json:"periodLabel"`
	Clock       string  `json:"clock"`
	Description string  `json:"description"`
	AthleteID   string  `json:"athleteId,omitempty"`
	PlayerName  string  `json:"playerName,omitempty"`
	HeadshotURL string  `json:"headshotUrl,omitempty"`
}

type SportsWinProbabilityPoint struct {
	PlayID      string  `json:"playId"`
	Period      int     `json:"period"`
	PeriodLabel string  `json:"periodLabel"`
	Clock       string  `json:"clock"`
	Home        float64 `json:"home"`
}

type SportsCricketScorecard struct {
	Kind     string                `json:"kind"`
	Innings  int                   `json:"innings"`
	TeamID   string                `json:"teamId,omitempty"`
	TeamName string                `json:"teamName"`
	Extras   string                `json:"extras,omitempty"`
	Players  []SportsCricketPlayer `json:"players"`
}

type SportsCricketPlayer struct {
	HeadshotURL string              `json:"headshotUrl,omitempty"`
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Dismissal   string              `json:"dismissal,omitempty"`
	Stats       []SportsCricketStat `json:"stats"`
}

type SportsCricketStat struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type SportsPregame struct {
	SeasonLabel     string                `json:"seasonLabel,omitempty"`
	UpdatedAt       time.Time             `json:"updatedAt"`
	Teams           []SportsPregameTeam   `json:"teams"`
	Comparisons     []SportsComparison    `json:"comparisons,omitempty"`
	PreviousMeeting *SportsPregameMeeting `json:"previousMeeting,omitempty"`
}

type SportsPregameTeam struct {
	TeamID           string                  `json:"teamId"`
	Record           string                  `json:"record,omitempty"`
	HomeRecord       string                  `json:"homeRecord,omitempty"`
	AwayRecord       string                  `json:"awayRecord,omitempty"`
	ConferenceRecord string                  `json:"conferenceRecord,omitempty"`
	Rank             int                     `json:"rank,omitempty"`
	StandingLabel    string                  `json:"standingLabel,omitempty"`
	StandingStats    []SportsPlayerStatistic `json:"standingStats,omitempty"`
	Recent           []SportsPregameResult   `json:"recent,omitempty"`
	Leaders          []SportsPregamePlayer   `json:"leaders,omitempty"`
	Probables        []SportsPregamePlayer   `json:"probables,omitempty"`
}

type SportsPregameResult struct {
	ID       string    `json:"id"`
	Date     time.Time `json:"date"`
	Opponent string    `json:"opponent"`
	Result   string    `json:"result"`
	Score    string    `json:"score,omitempty"`
}

type SportsPregamePlayer struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

type SportsPregameMeeting struct {
	Date       time.Time `json:"date"`
	HomeTeamID string    `json:"homeTeamId"`
	AwayTeamID string    `json:"awayTeamId"`
	HomeScore  string    `json:"homeScore"`
	AwayScore  string    `json:"awayScore"`
}
