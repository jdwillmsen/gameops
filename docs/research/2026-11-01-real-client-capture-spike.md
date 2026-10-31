# Real Bedrock client capture: spike

Jira JDW-325 (epic JDW-320). Time-boxed desk spike, 2026-10-31. Question: can
agents get true-pixel video of a real Bedrock client from the Linux devbox, and
at what cost? No licensed client, APK or login was used or needed.

## Recommendation

- **Linux client on the devbox (options 1, 4, 5): no-go.** Nothing there can run
  a 1.26.60 client and join our servers today (see below).
- **Windows VM on the devbox (option 2): no-go.** The devbox is itself a VM
  with no GPU and no IOMMU; a nested VM would draw in software at slideshow
  speed.
- **Go for an owner-assisted evidence session (option 3).** Agents script the
  server side (spawn, tame, route, console commands), the owner records their
  own Windows client with OBS. About 0.5 day to build the helper; about 20
  minutes of owner time per session. Next step: a Task under JDW-320 (see
  "Follow-up").
- Keep the rendered camera and Blockbench clips (JDW-323, JDW-324) as the
  automated evidence path. True-pixel video is for the final proof only.

## Findings that decide it

1. **The 1.26.60 client speaks NetherNet only.** Owner capture on 2026-10-30
   showed TCP SYNs and no UDP; public notes for Bedrock 26.60 (released
   2026-10-27) say RakNet joins now fail. So the RakNet preview server
   (`tools/lab-preview`) cannot be joined by a current client at all, whichever
   platform it runs on. Only a client on an older version (<= 1.26.5x) can join
   it. The live lab is NetherNet, so it is the only server a 1.26.60 client can
   join.
2. **The Linux launcher is behind and unproven for NetherNet.** mcpelauncher
   (minecraft-linux) is "unstable, unofficial"; its README table stops at
   1.21.73 and a guide quotes 1.26.31 as the highest on x86_64. Its tracker has
   open NetherNet issues: LAN worlds not listed (1.26.5x, opened 2026-09-20),
   LAN join fails with `InitialConnection-95` (2026-10-12), Realms signalling
   never completes (2026-08-13). No issue or release mentions 26.60. The
   maintainer says the launcher downloads the APK from Google Play with the
   owner's account; custom APKs are unsupported.
3. **The devbox cannot render a client.** `systemd-detect-virt` = `kvm`; the
   only display device is a QEMU std VGA (`1234:1111`); `/sys/kernel/iommu_groups`
   is empty; no NVIDIA driver. Mesa software GL only. Nested KVM is enabled
   (`kvm_amd nested=1`), so a VM would run, but without a GPU.
4. **Capture itself is cheap.** Measured below.

## Measurements (all in /tmp/jdw325, deleted afterwards)

| Test | Command (abridged) | Result |
|---|---|---|
| Xvfb 1280x720 capture + encode | `ffmpeg -f x11grab -framerate 30 -video_size 1280x720 -i :77 -t 10 -c:v libx264 -preset veryfast` | 300 frames in 10.2 s, 1.6 s CPU: capturing is not the bottleneck |
| Software WebGL2 draw | Chrome via Playwright, `--use-angle=swiftshader`, 20k triangles with a heavy fragment shader at 1280x720 | **0.1 FPS** |

The second number is a deliberately heavy synthetic shader, run on a box
already at load average ~12 (live preview server and crew containers). It is
not a Minecraft figure; it only shows that software GL cost is dominated by
fragment work and the 8 shared cores are not free. Minecraft 1.26 needs
OpenGL ES 3.0+ (Vibrant Visuals needs 3.1); 15 fps at 720p in software is
unlikely and was not demonstrable without the client. Not measured: a real
client, because none can be obtained without the owner's licence.

## Options

| # | Option | Feasible now | Effort | Licence / legal | Frame rate | Automation | Main risks |
|---|---|---|---|---|---|---|---|
| 1 | mcpelauncher under Xvfb, software GL | **No**: no 26.60 build known, NetherNet join bugs open, RakNet gone for 26.60 | 2+ days to find out, likely dead end | Owner's Google Play account must download the APK (OWNER STEP); unofficial, ToS grey | Software GL: probably under 5 fps | Good if it ran (xdotool + x11grab) | Version lag, account risk, Play login on a headless box |
| 2 | Windows VM under KVM | **No** on this box: nested, no GPU, no IOMMU | 2-3 days plus a GPU host | Needs a Windows licence and the owner's Microsoft login in the VM | Software only: slideshow | Hard: UI automation of Bedrock (joining, camera) is brittle | The 5090 VM runs vLLM (about 30 of 32 GB VRAM); passing it through or borrowing it for a Windows guest is an owner decision and out of scope |
| 3 | Owner's Windows client plus OBS | **Yes** | 0.5 day helper, 20 min owner time per session | Clean: owner's own licence and PC | 60 fps native | Server side fully scriptable; owner only joins and presses record (or OBS WebSocket) | Needs the owner present; PC and lab on the LAN |
| 4 | Mirror proxy (real client sees a unit's view) | **Unproven.** Needs a NetherNet server-side proxy | 3+ sessions; the old brief assumed RakNet | Needs a client anyway (option 3 supplies it) | Same as the client used | Medium | gophertunnel NetherNet listener maturity; client movement prediction versus replayed packets; unit's NetherNet login is the owner's device-code step |
| 5 | Cheap extras | Spectator or console camera driven from the server; rendered camera (JDW-323); Blockbench clips (JDW-324) | Already planned | Clean | Rendered: ours | Full | Not true-pixel: custom entities are stand-in boxes unless the pack is loaded |

Cloud Android streaming: not evaluated; no free licence-clean service was
found and the rules exclude accounts and purchases.

## Recommended recipe: owner-assisted evidence session

Server side (agents, scripted; lab only, never FWB):

1. Join address is the lab NetherNet one from the owner's saved entry
   (`192.168.1.163:31137`, LAN only).
2. An agent script, run through the existing console and `scriptevent` hooks,
   places the six animals in a flat arena, sets time to day, clears weather and
   runs a fixed route per scenario (approach, tame, sit/stand, follow, aura).
3. The route is announced in chat/title text so the video is self-captioned.
4. `timeline.jsonl` from JDW-321 timestamps each step, so clips can be cut
   afterwards with ffmpeg without the owner doing anything.

OWNER STEPS (in the owner's own Windows terminal and apps; no credentials go to
an agent):

1. Install OBS Studio (free, obsproject.com) if absent. Scene: "Game Capture"
   of Minecraft, 1920x1080, 60 fps, output `mkv`, encoder NVENC or x264.
2. Optional, for scripted start/stop: OBS > Tools > WebSocket Server Settings >
   enable, set a password only you know. Agents do not need it for session 1;
   the owner presses record.
3. Launch Minecraft 1.26.60, join the lab address above, spawn point arena.
4. Tell the agent thread "ready"; press Start Recording when it posts the
   countdown; stop on "done".
5. Copy the `.mkv` to the devbox (`scp video.mkv dev-admin@<devbox>:~/evidence/wildlife/<run-id>/videos/`),
   where the evidence-bundle contract (JDW-320 spec) picks it up.

Players-online and restarts on the lab are already approved by the owner; this
recipe needs neither.

## Effort estimate

| Item | Effort |
|---|---|
| Arena + route script and chat captions (reuses JDW-321 hooks) | 0.5 day |
| ffmpeg cutter driven by `timeline.jsonl` | 0.25 day |
| Optional OBS WebSocket start/stop from the Windows PC | 0.25 day |
| Per-session owner time | about 20 min |

## Risks

- NetherNet is LAN-bound for the owner; a remote session would need the
  HAProxy UDP rework noted in the nethernet-only-windows-client note.
- Custom animals drawn by the real client depend on the lab resource pack
  being delivered; the real client is the best test of that.
- Linux launcher claims above come from public pages and trackers read on
  2026-10-31, not from a run. Re-check the tracker if a future release adds
  NetherNet support; the estimate then drops to about 1 day for a client under
  Xvfb, still needing the owner's Play login.

## Follow-up

Task under JDW-320: "Owner-assisted evidence session helper" (arena and route
script, caption titles, ffmpeg cutter, optional OBS WebSocket start/stop).
Depends on JDW-321 hooks.
