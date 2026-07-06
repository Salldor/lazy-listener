# lazy-listener

Records audio from a microphone, detects speech in real time using WebRTC VAD, and transcribes it to text with [whisper.cpp](https://github.com/ggml-org/whisper.cpp). Optionally identifies speakers across segments (diarization) using [Resemblyzer](https://github.com/resemble-ai/Resemblyzer). Works in Russian out of the box. Saves both the raw recording (WAV) and the transcript (TXT).

## Prerequisites

### 1. Homebrew

If you don't have Homebrew:

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

### 2. Go ≥ 1.22

```bash
brew install go
```

Verify: `go version`

### 3. whisper-cpp

```bash
brew install whisper-cpp
```

This installs the `whisper-server` binary that lazy-listener uses for transcription.

### 4. Python dependencies (optional, for speaker diarization)

Required only if you plan to use the `-diarize` flag:

```bash
pip install resemblyzer flask soundfile
```

## Setup

### Clone the repo

```bash
git clone https://github.com/Salldor/lazy-listener.git
cd lazy-listener
```

### Download a model

Models are not included in the repo. Download one into the `models/` directory.

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

No extra flags or system libraries required beyond what Homebrew installs.

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
```

| Flag              | Default                   | Description                                        |
|-------------------|---------------------------|----------------------------------------------------|
| `-model`          | `./models/ggml-small.bin` | Path to the ggml model file                        |
| `-lang`           | `ru`                      | Transcription language (`ru`, `en`, `de`, `fr`, …) |
| `-diarize`        | `false`                   | Enable speaker diarization (requires Python deps)  |
| `-min-rms`        | `0.005`                   | Minimum RMS energy; segments below this are skipped|
| `-min-speech-ms`  | `90`                      | Minimum speech duration in ms before transcribing  |

### What happens at startup

```
Available capture devices:
  [1] BlackHole 2ch
  [2] MacBook Pro Microphone
  [3] ZoomAudioDevice
Select capture device [1-3]: 2          ← pick your microphone

Available playback devices:
  [1] BlackHole 2ch
  [2] MacBook Pro Speakers
Select playback device (Enter to skip):  ← Enter to disable monitoring

Transcript → transcripts/transcript_2026_07_06_00_35_11.txt
Recording  → recordings/rec_2026_07_06_00_35_11.wav
Loading model... ready.

Recording... Press Ctrl+C to stop.
```

With `-diarize`:

```
Loading model... ready.
Starting speaker tracker... ready.

Recording... Press Ctrl+C to stop.
[00:03 → 00:07] Speaker 1: Привет, сегодня поговорим о проекте.
[00:09 → 00:14] Speaker 2: Согласен, с чего начнём?
```

Speak in Russian. After each pause the transcript appears in the terminal and is appended to the text file.

### Stopping

Press **Ctrl+C**. The WAV file header is finalized on exit so the recording is always valid.

## Output files

| Directory     | Contents                                      |
|---------------|-----------------------------------------------|
| `recordings/` | `rec_YYYY_MM_DD_HH_MM_SS.wav` — raw audio     |
| `transcripts/`| `transcript_YYYY_MM_DD_HH_MM_SS.txt` — text   |

Both directories are created automatically on first run.

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
```

- **WebRTC VAD** classifies each 30 ms audio frame as speech or silence.
- A segment is sent for transcription after ~700 ms of silence following speech.
- **300 ms pre-roll** is prepended so the beginning of words is never cut off.
- `whisper-server` runs as a local subprocess — the model loads once and stays in memory for the entire session.
- `speaker_tracker.py` (when `-diarize` is set) maintains a gallery of voice embeddings (Resemblyzer GE2E) across the entire session. Each new segment is compared against known voices via cosine similarity; a new speaker ID is assigned when no match exceeds the threshold (0.75 by default). Both HTTP requests run in parallel per segment.

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

**Speaker diarization shows `Speaker ?`**  
The speaker tracker returned an error. Check the log:
  ```bash
  tail -f speaker-tracker.log
  ```
Common cause: missing Python dependencies — run `pip install resemblyzer flask soundfile`.

**All speech is attributed to the same speaker**  
The cosine similarity threshold may be too low for the voices in the session. The default is 0.75; lower values make the tracker more permissive (fewer new speakers assigned). You can change it by editing `speaker_tracker.py` and adjusting the `--threshold` default, or by modifying `startSpeakerTracker()` in `cmd/transcribe.go` to pass a different value.
