# lazy-listener

Records audio from a microphone, detects speech in real time using WebRTC VAD, and transcribes it to text with [whisper.cpp](https://github.com/ggml-org/whisper.cpp). Works in Russian out of the box. Saves both the raw recording (WAV) and the transcript (TXT).

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
```

| Flag     | Default                      | Description                                      |
|----------|------------------------------|--------------------------------------------------|
| `-model` | `./models/ggml-small.bin`    | Path to the ggml model file                      |
| `-lang`  | `ru`                         | Transcription language (`ru`, `en`, `de`, `fr`, …) |

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
                └──▶ whisper-server (HTTP, model kept in memory)
                       │
                       └──▶ text ──▶ console + transcript file
```

- **WebRTC VAD** classifies each 30 ms audio frame as speech or silence.  
- A segment is sent for transcription after ~700 ms of silence following speech.  
- **300 ms pre-roll** is prepended so the beginning of words is never cut off.  
- `whisper-server` runs as a local subprocess — the model loads once and stays in memory for the entire session.

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
