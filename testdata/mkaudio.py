"""Generate real WebM/Opus files - the exact container the browser produces."""
import av, numpy as np, sys

def write_webm(path, samples, rate=48000):
    container = av.open(path, mode="w", format="webm")
    stream = container.add_stream("libopus", rate=rate)
    stream.layout = "mono"
    frame_size = 960
    for i in range(0, len(samples), frame_size):
        block = samples[i:i+frame_size]
        if len(block) < frame_size:
            block = np.pad(block, (0, frame_size - len(block)))
        frame = av.AudioFrame.from_ndarray(block.reshape(1, -1), format="s16", layout="mono")
        frame.sample_rate = rate
        frame.pts = i
        for packet in stream.encode(frame):
            container.mux(packet)
    for packet in stream.encode(None):
        container.mux(packet)
    container.close()

rate = 48000
# 3 seconds of silence
write_webm("/out/silence.webm", np.zeros(rate*3, dtype=np.int16), rate)
# 0.1 seconds - below the min_audio_seconds floor
write_webm("/out/tooshort.webm", np.zeros(int(rate*0.1), dtype=np.int16), rate)
print("written")
