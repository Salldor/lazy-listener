#!/usr/bin/env python3
import argparse
import io
import sys
import traceback
import numpy as np
import soundfile as sf

parser = argparse.ArgumentParser()
parser.add_argument('--port', type=int, default=18766)
parser.add_argument('--threshold', type=float, default=0.75)
args = parser.parse_args()

from flask import Flask, request, jsonify
from resemblyzer import VoiceEncoder, preprocess_wav

app = Flask(__name__)
encoder = VoiceEncoder()
gallery = {}   # speaker_id -> mean embedding
counter = [0]
threshold = args.threshold


def cosine_sim(a, b):
    denom = np.linalg.norm(a) * np.linalg.norm(b)
    if denom < 1e-9:
        return 0.0
    return float(np.dot(a, b) / denom)


@app.route('/identify', methods=['POST'])
def identify():
    if 'file' not in request.files:
        return jsonify({'error': 'no file'}), 400
    wav_bytes = request.files['file'].read()
    try:
        audio_data, sr = sf.read(io.BytesIO(wav_bytes), dtype='float32')
        if audio_data.ndim > 1:
            audio_data = audio_data.mean(axis=1)
        wav = preprocess_wav(audio_data, source_sr=sr)
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        return jsonify({'error': str(e)}), 500

    if len(wav) < 8000:  # < 0.5 s at 16 kHz — too short for reliable embedding
        return jsonify({'speaker': 'Speaker 1'})

    emb = encoder.embed_utterance(wav)

    best_id, best_sim = None, -1.0
    for spk_id, mean_emb in gallery.items():
        sim = cosine_sim(emb, mean_emb)
        if sim > best_sim:
            best_sim, best_id = sim, spk_id

    if best_id and best_sim >= threshold:
        gallery[best_id] = (gallery[best_id] + emb) / 2.0
        return jsonify({'speaker': best_id})

    counter[0] += 1
    new_id = f'Speaker {counter[0]}'
    gallery[new_id] = emb
    return jsonify({'speaker': new_id})


@app.route('/reset', methods=['POST'])
def reset():
    gallery.clear()
    counter[0] = 0
    return jsonify({'status': 'ok'})


if __name__ == '__main__':
    app.run(host='127.0.0.1', port=args.port, debug=False)
