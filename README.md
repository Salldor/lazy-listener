# lazy-listener

Records audio from a microphone, detects speech in real time using WebRTC VAD, and transcribes it to text with [whisper.cpp](https://github.com/ggml-org/whisper.cpp). Optionally identifies speakers across segments (diarization) using [Resemblyzer](https://github.com/resemble-ai/Resemblyzer). After the recording stops, automatically generates a structured meeting recap using a local LLM via [Ollama](https://ollama.com). Works in Russian out of the box. Saves the raw recording (WAV), the transcript (TXT), and the recap (MD).

## Prerequisites

### 1. Homebrew

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

### 2. Go ≥ 1.22

```bash
brew install go
```

### 3. whisper-cpp

```bash
brew install whisper-cpp
```

This installs the `whisper-server` binary used for transcription.

### 4. Ollama (for recap generation)

```bash
brew install ollama
ollama pull gemma4       # or any other model, e.g. gemma3:12b
ollama serve             # start the local server
```

The recap is generated automatically after each recording session. If Ollama is not running, a warning is printed and the transcript is still saved normally.

### 5. Python dependencies (optional, for speaker diarization)

Required only with the `-diarize` flag:

```bash
pip install resemblyzer flask soundfile
```

## Setup

### Clone the repo

```bash
git clone https://github.com/Salldor/lazy-listener.git
cd lazy-listener
```

### Download a Whisper model

```bash
mkdir -p models
```

**Recommended for Russian — medium (1.5 GB):**

```bash
curl -L -o models/ggml-medium.bin \
  "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium.bin"
```

**Faster but less accurate — small (466 MB):**

```bash
curl -L -o models/ggml-small.bin \
  "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin"
```

| Model  | Size   | Russian quality | Speed (M3 Pro) |
|--------|--------|-----------------|----------------|
| small  | 466 MB | acceptable      | ~2s/segment    |
| medium | 1.5 GB | good            | ~5s/segment    |
| large  | 3.1 GB | best            | ~10s/segment   |

### Build

```bash
make build
```

## Usage

```bash
make run
```

Or with explicit flags:

```bash
./lazy-listener -model ./models/ggml-medium.bin -lang ru
./lazy-listener -model ./models/ggml-medium.bin -lang en

# with speaker diarization
./lazy-listener -model ./models/ggml-medium.bin -diarize

# specify meeting type for the recap
./lazy-listener -model ./models/ggml-medium.bin -meeting-type grooming
./lazy-listener -model ./models/ggml-medium.bin -meeting-type planning
```

### All flags

| Flag              | Default                   | Description                                           |
|-------------------|---------------------------|-------------------------------------------------------|
| `-model`          | `./models/ggml-small.bin` | Path to the ggml model file                           |
| `-lang`           | `ru`                      | Transcription language (`ru`, `en`, `de`, `fr`, …)   |
| `-diarize`        | `false`                   | Enable speaker diarization (requires Python deps)     |
| `-min-rms`        | `0.005`                   | Minimum RMS energy; segments below this are skipped   |
| `-min-speech-ms`  | `90`                      | Minimum speech duration in ms before transcribing     |
| `-silence-ms`     | `700`                     | Silence duration in ms that ends a speech segment     |
| `-max-segment-ms` | `30000`                   | Hard cap on segment duration in ms                    |
| `-meeting-type`   | `general`                 | Meeting type for recap: `general`, `grooming`, `planning` |
| `-recap-model`    | `gemma4:latest`           | Ollama model used for recap generation                |
| `-ollama`         | `http://localhost:11434`  | Ollama server base URL                                |

### Meeting types

The recap prompt is tailored to the type of meeting:

| Type       | Focus                                                                              |
|------------|------------------------------------------------------------------------------------|
| `general`  | Main topics, key decisions, action items                                           |
| `grooming` | Tasks discussed, priorities, blockers, assignments, sprint goals                   |
| `planning` | Sprint goals, committed tasks, ownership, risks, decisions made                    |

The recap is written in the same language as the transcript (Russian → Russian, English → English).

### What happens at startup

```
Available capture devices:
  [1] BlackHole 2ch
  [2] MacBook Pro Microphone
Select capture device [1-2]: 2

Available playback devices:
  [1] MacBook Pro Speakers
Select playback device (Enter to skip):

Transcript → transcripts/transcript_2026_07_06_10_30_00.txt
Recording  → recordings/rec_2026_07_06_10_30_00.wav
Loading model... ready.

Recording... Press Ctrl+C to stop.
```

### Stopping

Press **Ctrl+C**. The shutdown sequence:

1. Audio capture stops.
2. Any in-flight transcription segments are drained and written to the transcript.
3. WAV file header is finalized — the recording is always a valid file.
4. Recap is generated from the completed transcript and saved to `recaps/`.

```
Stopping...

Generating grooming recap using gemma4:latest...

--- RECAP ---
## Tasks Discussed
...

Saved → recaps/recap_2026_07_06_10_30_00_grooming.md
```

### Process existing transcripts

You can also generate a recap for any existing transcript file without recording:

```bash
./lazy-listener recap \
  -transcript transcripts/transcript_2026_07_06_10_30_00.txt \
  -type grooming \
  -model gemma4:latest
```

## Output files

| Directory     | Contents                                                       |
|---------------|----------------------------------------------------------------|
| `recordings/` | `rec_YYYY_MM_DD_HH_MM_SS.wav` — raw audio                     |
| `transcripts/`| `transcript_YYYY_MM_DD_HH_MM_SS.txt` — timestamped transcript |
| `recaps/`     | `recap_YYYY_MM_DD_HH_MM_SS_<type>.md` — meeting recap         |

All directories are created automatically on first run.

## How it works

```
Microphone
  │
  ├──▶ WAV file  (every captured frame)
  │
  └──▶ WebRTC VAD (30 ms frames)
         │
         └──▶ speech segment detected
                │
                ├──▶ whisper-server  (HTTP :18765) ──▶ text  ┐
                │     model kept in memory                    ├──▶ console + transcript file
                └──▶ speaker-tracker (HTTP :18766) ──▶ label ┘
                      voice gallery kept in memory

  On Ctrl+C:
  └──▶ transcript file
         │
         └──▶ Ollama /api/chat ──▶ recap (Markdown)
               local LLM (Gemma or other)
```

- **WebRTC VAD** classifies each 30 ms audio frame as speech or silence.
- A segment is sent for transcription after ~700 ms of silence following speech.
- **300 ms pre-roll** is prepended so the beginning of words is never cut off.
- `whisper-server` runs as a local subprocess — the model loads once and stays in memory for the entire session.
- `speaker_tracker.py` (when `-diarize` is set) maintains a gallery of voice embeddings (Resemblyzer GE2E) across the session. Each segment is compared against known voices via cosine similarity; a new speaker ID is assigned when no match exceeds the threshold (0.75 by default).
- After Ctrl+C, the full transcript is sent to a local Ollama model with a type-specific system prompt to produce the structured recap.

## Microphone permissions

On first run macOS will ask for microphone access. Grant it in **System Settings → Privacy & Security → Microphone**.

## Troubleshooting

**`whisper-server: command not found`**  
Run `brew install whisper-cpp`.

**`stat ./models/ggml-medium.bin: no such file or directory`**  
Download the model as shown in the Setup section.

**No transcription output despite speaking**  
- Check that the correct capture device is selected.
- Speak closer to the microphone and pause between sentences — VAD needs a clear silence to trigger transcription.
- Tail `whisper-server.log` to see what the server is doing:
  ```bash
  tail -f whisper-server.log
  ```

**Recap generation failed: ollama request failed (is ollama running?)**  
Start the Ollama server with `ollama serve`, or pull the model first: `ollama pull gemma4`.

**Speaker diarization shows `Speaker ?`**  
The speaker tracker returned an error. Check the log:
```bash
tail -f speaker-tracker.log
```
Common cause: missing Python dependencies — run `pip install resemblyzer flask soundfile`.

**All speech is attributed to the same speaker**  
The cosine similarity threshold may be too low. The default is 0.75; lower values make the tracker more permissive. Adjust via `--threshold` in `startSpeakerTracker()` in `cmd/transcribe.go`.
