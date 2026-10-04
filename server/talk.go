package main

// Talking to a plant: the website uploads a voice recording to
// POST /api/talk/{id} and gets a job back. One at a time, in the order they
// came in, the server turns each recording into text (ElevenLabs), asks
// Gemini for the plant's answer in its personality (personalities.json),
// knowing its live readings, and turns the answer into the plant's voice
// (ElevenLabs). The page asks GET /api/talk/jobs/{job} until the answer is
// ready, then plays GET /api/talk/jobs/{job}/voice.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"strings"
	"sync"
	"time"

	elstt "github.com/plexusone/elevenlabs-go/stt"
)

const (
	historyLen = 10              // messages a plant remembers between recordings, for follow-up questions
	queueLen   = 20              // recordings waiting at most; more are turned away
	keepAnswer = 5 * time.Minute // how long a finished answer waits for its page to fetch it
)

var (
	errTalkOff  = errors.New("talking is off: set GEMINI_API_KEY and ELEVENLABS_API_KEY in server/.env")
	errTalkFull = errors.New("lots of people are talking to the plants right now, try again in a minute")
	errNoJob    = errors.New("that answer doesn't exist or has expired")
	errNotHeard = errors.New("I didn't catch that, try again a little closer to the mic")
)

// talkJob is one recording on its way to an answer. The exported fields are
// what GET /api/talk/jobs/{job} returns.
type talkJob struct {
	Status   string `json:"status"`             // queued, working, done or failed
	Position int    `json:"position,omitempty"` // while queued: recordings ahead of this one
	Heard    string `json:"heard,omitempty"`
	Answer   string `json:"answer,omitempty"`
	Voice    bool   `json:"voice"` // the answer can be played from .../voice
	Error    string `json:"error,omitempty"`

	seq      int
	plant    Plant
	audio    []byte // the recording, until it's been heard
	ctype    string
	voice    []byte // the answer, as MP3
	finished time.Time
}

// Talker runs the conversations. It's off (nil clients) when the keys aren't set.
type Talker struct {
	stt     *STTManager
	gemini  *GeminiBackend
	tts     *TTSManager
	sensors *LatestReading
	queue   chan *talkJob

	mu      sync.Mutex
	jobs    map[string]*talkJob
	nextSeq int

	history map[string][]ChatMessage // per plant; only the worker touches it
}

func newTalker(sensors *LatestReading) *Talker {
	t := &Talker{sensors: sensors, queue: make(chan *talkJob, queueLen),
		jobs: map[string]*talkJob{}, history: map[string][]ChatMessage{}}
	geminiKey, elevenKey := getAPIKey(), getElevenLabsKey()
	if geminiKey == "" || elevenKey == "" {
		log.Print("talk: GEMINI_API_KEY or ELEVENLABS_API_KEY isn't set, so talking to plants is off")
		return t
	}
	gemini, err := NewGeminiBackend(geminiKey, envOr("GEMINI_MODEL", loadKeyFromEnvFile(".env", "GEMINI_MODEL")))
	if err != nil {
		log.Printf("talk: %v", err)
		return t
	}
	stt, err := NewSTTManager(elevenKey)
	if err != nil {
		log.Printf("talk: %v", err)
		return t
	}
	tts, err := NewTTSManager(elevenKey)
	if err != nil {
		log.Printf("talk: %v", err)
		return t
	}
	t.gemini, t.stt, t.tts = gemini, stt, tts
	go t.work()
	log.Printf("talk: on, with %s", gemini.ModelName())
	return t
}

func (t *Talker) On() bool { return t.gemini != nil }

// Submit queues a recording (audio, of type contentType) for plant p and
// returns its job's ID.
func (t *Talker) Submit(p Plant, audio []byte, contentType string) (string, error) {
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.forgetOld()
	t.nextSeq++
	job := &talkJob{Status: "queued", seq: t.nextSeq, plant: p, audio: audio, ctype: contentType}
	select {
	case t.queue <- job:
	default:
		return "", errTalkFull
	}
	t.jobs[id] = job
	return id, nil
}

// Job is the job's state for its page, and its voice once it's done.
func (t *Talker) Job(id string) (talkJob, []byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	job, ok := t.jobs[id]
	if !ok {
		return talkJob{}, nil, false
	}
	state := *job
	if job.Status == "queued" {
		for _, other := range t.jobs {
			if (other.Status == "queued" || other.Status == "working") && other.seq < job.seq {
				state.Position++
			}
		}
	}
	return state, job.voice, true
}

// forgetOld drops answers nobody fetched in time. t.mu must be held.
func (t *Talker) forgetOld() {
	for id, job := range t.jobs {
		if !job.finished.IsZero() && time.Since(job.finished) > keepAnswer {
			delete(t.jobs, id)
		}
	}
}

// work answers the queued recordings, one at a time.
func (t *Talker) work() {
	for job := range t.queue {
		t.mu.Lock()
		job.Status = "working"
		t.mu.Unlock()

		heard, answer, voice, err := t.answer(job)

		t.mu.Lock()
		job.Heard, job.Answer, job.voice, job.Voice = heard, answer, voice, voice != nil
		job.audio, job.finished = nil, time.Now()
		switch {
		case answer != "": // even without a voice, the page can show the answer
			job.Status = "done"
		case errors.Is(err, errNotHeard):
			job.Status, job.Error = "failed", err.Error()
		default:
			job.Status, job.Error = "failed", "the plant couldn't answer just now, try again"
		}
		t.mu.Unlock()
		if err != nil {
			log.Printf("talk: %s: %v", job.plant.ID, err)
		}
	}
}

// answer hears the recording, thinks of the plant's answer and says it.
func (t *Talker) answer(job *talkJob) (heard, answer string, voice []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	res, err := t.stt.client.STT().Transcribe(ctx, &elstt.Request{
		FileBytes: job.audio,
		FileName:  "speech" + audioExt(job.ctype),
		ModelID:   "scribe_v2",
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("speech to text: %w", err)
	}
	heard = strings.TrimSpace(res.Text)
	if heard == "" {
		return "", "", nil, errNotHeard
	}

	p := job.plant
	persona := personaFor(p)
	withSenses := *persona
	reading, ok := t.sensors.Get()
	withSenses.Instruction += "\n\n" + senses(p, reading, ok)
	answer, err = t.gemini.Chat(ctx, &withSenses, t.history[p.ID], heard)
	if err != nil {
		return heard, "", nil, fmt.Errorf("gemini: %w", err)
	}
	answer = strings.TrimSpace(answer)
	t.history[p.ID] = lastN(append(t.history[p.ID],
		ChatMessage{Role: "user", Content: heard},
		ChatMessage{Role: "assistant", Content: answer}), historyLen)
	log.Printf("talk: %s heard %q, answers %q", p.ID, heard, answer)

	voice, err = t.say(ctx, persona, answer)
	if err != nil {
		return heard, answer, nil, fmt.Errorf("voice: %w", err)
	}
	return heard, answer, voice, nil
}

// say is the plant saying text in its ElevenLabs voice (tts.go), as MP3.
func (t *Talker) say(ctx context.Context, persona *Personality, text string) ([]byte, error) {
	words := CleanSpokenText(persona.Name, text)
	audio, err := t.tts.client.TTS().Simple(ctx, ResolveVoiceID(persona), words)
	if err != nil { // a voice this account can't use: fall back, like tts.go does
		fallback := DefaultVoiceFern
		if strings.EqualFold(persona.ID, "spike") {
			fallback = DefaultVoiceSpike
		}
		audio, err = t.tts.client.TTS().Simple(ctx, fallback, words)
	}
	if err != nil {
		return nil, err
	}
	return io.ReadAll(audio)
}

// personaFor is the plant's personality from personalities.json, or a
// plain one when the plant doesn't have one.
func personaFor(p Plant) *Personality {
	if list, err := LoadPersonalities(); err == nil {
		for _, persona := range list {
			if strings.EqualFold(persona.ID, p.ID) {
				return persona
			}
		}
	}
	return &Personality{ID: p.ID, Name: p.Name,
		Instruction: "You are " + p.Name + ", a houseplant. Keep spoken responses under two sentences."}
}

// senses tells the plant how it's doing right now, so it answers from its
// real soil, light and air rather than making them up.
func senses(p Plant, r Reading, ok bool) string {
	if !ok || r.Stale() {
		return "Your sensors aren't reporting right now, so you can't feel your soil, light or air."
	}
	var parts []string
	if r.MoisturePct != nil {
		parts = append(parts, fmt.Sprintf("soil moisture %.0f%% (you like %.0f-%.0f%%)", *r.MoisturePct, p.MoistureMinPct, p.MoistureMaxPct))
	}
	if r.LightPct != nil {
		parts = append(parts, fmt.Sprintf("light %.0f%% (you need at least %.0f%%)", *r.LightPct, p.LightMinPct))
	}
	if r.TemperatureC != nil {
		parts = append(parts, fmt.Sprintf("air temperature %.1f °C (you like %.0f-%.0f °C)", *r.TemperatureC, p.TempMinC, p.TempMaxC))
	}
	feeling := map[string]string{
		"ok": "doing well", "thirsty": "thirsty", "overwatered": "overwatered",
		"cold": "too cold", "hot": "too hot", "dark": "not getting enough light",
	}[p.Status(r, ok)]
	return "Right now your sensors read: " + strings.Join(parts, ", ") + ". Overall you are " + feeling + "."
}

// audioExt is the file extension ElevenLabs expects for a browser recording.
// Chrome records WebM; Safari records MP4.
func audioExt(contentType string) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac":
		return ".m4a"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	}
	return ".webm"
}

func lastN(m []ChatMessage, n int) []ChatMessage {
	if len(m) > n {
		return m[len(m)-n:]
	}
	return m
}
