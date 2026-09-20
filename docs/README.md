# docs

## crt-effect.png

The retro terminal effect, as rendered by the real code.

It is a capture of the actual interface, not a mockup: a pty ran the built binary, the stream it
wrote was fed into a screen emulator that keeps style as well as text, and each cell was painted
with the colour the program had emitted for it. The scanlines, the vignette and the phosphor are
therefore whatever the code really produced on that frame.

The model answering in this capture is the repository's mock (`tools/mockllm`), so the text is a
canned reply. The same effect was measured running against Ollama Cloud on the target i386
netbook — see the skill notes on the measured scanline ratio (0.62 measured against 0.65
configured).
