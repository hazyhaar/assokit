/**
 * visio.js — enclave VanJS pour la salle de visioconférence assokit (L4).
 *
 * Chargé par /salon/{slug}/visio uniquement. Lit la config depuis
 * <script id="visio-config" type="application/json">.
 *
 * Dépendances locales (zéro CDN) :
 *   - /static/js/van.js         (VanJS 1.5.2)
 *   - /static/js/livekit-client.umd.min.js (livekit-client 2.5.3 UMD)
 *
 * FRONTIÈRE ASSUMÉE :
 *   Contrôles LOCAUX (micro, caméra, partage écran, lever la main)
 *   = appels WebRTC directs sur la LocalParticipant — aucun serveur impliqué.
 *   Contrôles HÔTE (couper/expulser un participant, terminer)
 *   = POST HTTP vers les routes /salon/{slug}/visio/{mute_participant|remove_participant|end}
 *     gardées owner (L2b). Ces routes appellent le Connector LiveKit côté serveur.
 */
(function () {
  "use strict";

  /* ------------------------------------------------------------------ */
  /* 1. Config injectée par le serveur (JSON échappé, jamais concaténé) */
  /* ------------------------------------------------------------------ */
  const cfgEl = document.getElementById("visio-config");
  if (!cfgEl) {
    document.body.innerHTML = "<p>Erreur : configuration visio absente.</p>";
    return;
  }
  let cfg;
  try {
    cfg = JSON.parse(cfgEl.textContent);
  } catch (e) {
    document.body.innerHTML = "<p>Erreur : configuration visio invalide.</p>";
    return;
  }
  // cfg = { token, url, room, slug, salonName, isOwner, csrfToken }

  /* ------------------------------------------------------------------ */
  /* 2. Imports des libs vendored                                        */
  /* ------------------------------------------------------------------ */
  const Van = window.van;
  // livekit-client UMD expose ses exports directement sur window.LivekitClient (globalThis).
  const LK = window.LivekitClient;

  if (!Van || !LK) {
    document.body.innerHTML = "<p>Erreur : bibliothèques locales non chargées (van=" + !!window.van + ", lk=" + !!window.LivekitClient + ").</p>";
    return;
  }

  const add = Van.add;
  const { div, button, video, audio, span, h1, h2, p, input, label, select, option } = Van.tags;

  const {
    Room,
    RoomEvent,
    Track,
    createLocalVideoTrack,
    createLocalAudioTrack,
  } = LK;

  /* ------------------------------------------------------------------ */
  /* 3. État réactif global (van.state)                                  */
  /* ------------------------------------------------------------------ */
  const state = {
    phase: Van.state("prejoin"), // "prejoin" | "room"
    micEnabled: Van.state(true),
    camEnabled: Van.state(true),
    screenSharing: Van.state(false),
    handRaised: Van.state(false),
    chatOpen: Van.state(false),
    displayName: Van.state(""),
    participants: Van.state([]), // [{ identity, audioMuted, videoMuted, isSpeaking }]
    chatMessages: Van.state([]), // [{ from, text }]
    chatInput: Van.state(""),
    devices: Van.state({ cameras: [], mics: [], speakers: [] }),
    selectedCamera: Van.state(""),
    selectedMic: Van.state(""),
    selectedSpeaker: Van.state(""),
    localVideoEl: null,  // <video> element pour la prévisualisation locale
    room: null,          // instance LiveKit Room
  };

  /* ------------------------------------------------------------------ */
  /* 4. Utilitaires                                                      */
  /* ------------------------------------------------------------------ */
  function post(path, body) {
    return fetch(path, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": cfg.csrfToken || "",
      },
      body: JSON.stringify(body),
    });
  }

  async function enumerateDevices() {
    try {
      const devs = await navigator.mediaDevices.enumerateDevices();
      const cameras = devs.filter((d) => d.kind === "videoinput");
      const mics = devs.filter((d) => d.kind === "audioinput");
      const speakers = devs.filter((d) => d.kind === "audiooutput");
      state.devices.val = { cameras, mics, speakers };
      if (cameras.length && !state.selectedCamera.val)
        state.selectedCamera.val = cameras[0].deviceId;
      if (mics.length && !state.selectedMic.val)
        state.selectedMic.val = mics[0].deviceId;
      if (speakers.length && !state.selectedSpeaker.val)
        state.selectedSpeaker.val = speakers[0].deviceId;
    } catch (_) {}
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  /* ------------------------------------------------------------------ */
  /* 5. Pré-visioconférence (PREJOIN)                                    */
  /* ------------------------------------------------------------------ */
  let localVideoTrack = null;
  let localAudioTrack = null;

  // startLocalPreview : démarre la prévisualisation caméra/micro locale.
  // Toujours async, jamais bloquant (les erreurs d'accès aux médias sont silencieuses).
  function startLocalPreview() {
    // Différé sur la prochaine micro-tâche pour ne pas bloquer le rendu initial.
    Promise.resolve().then(async function () {
      await enumerateDevices();
      try {
        localVideoTrack = await createLocalVideoTrack({
          deviceId: state.selectedCamera.val || undefined,
        });
        if (state.localVideoEl) {
          localVideoTrack.attach(state.localVideoEl);
        }
      } catch (_) {}
      try {
        localAudioTrack = await createLocalAudioTrack({
          deviceId: state.selectedMic.val || undefined,
        });
      } catch (_) {}
    }).catch(function () {});
  }

  function PrejoinView() {
    const videoEl = document.createElement("video");
    videoEl.autoplay = true;
    videoEl.muted = true;
    videoEl.playsInline = true;
    videoEl.style.cssText =
      "width:100%;max-width:480px;border-radius:8px;background:#111;";
    state.localVideoEl = videoEl;

    const nameInput = input({
      type: "text",
      placeholder: "Votre nom affiché",
      value: state.displayName.val,
      oninput: (e) => { state.displayName.val = e.target.value; },
      style: "font-size:1rem;padding:10px 14px;border-radius:6px;border:1px solid #ccc;width:100%;box-sizing:border-box;",
      "aria-label": "Nom affiché dans la salle",
    });

    function DeviceSelect(label_, stateRef, list) {
      return div(
        { style: "margin-bottom:8px;" },
        label({ style: "display:block;font-size:.875rem;margin-bottom:4px;" }, label_),
        Van.derive(() => {
          const devices = state.devices.val[list];
          const sel = select({
            style: "width:100%;padding:8px;border-radius:6px;border:1px solid #ccc;",
            onchange: (e) => { stateRef.val = e.target.value; },
          });
          devices.forEach((d) => {
            sel.appendChild(
              Object.assign(document.createElement("option"), {
                value: d.deviceId,
                textContent: d.label || d.deviceId,
                selected: d.deviceId === stateRef.val,
              })
            );
          });
          return sel;
        })
      );
    }

    const camToggle = button({
      type: "button",
      onclick: () => { state.camEnabled.val = !state.camEnabled.val; },
      style: "min-width:72px;min-height:72px;border-radius:50%;border:none;cursor:pointer;font-size:.875rem;margin:4px;",
      "aria-label": "Basculer la caméra",
    });
    Van.derive(() => {
      camToggle.textContent = state.camEnabled.val ? "Caméra ON" : "Caméra OFF";
      camToggle.style.background = state.camEnabled.val ? "#22c55e" : "#ef4444";
      camToggle.style.color = "#fff";
    });

    const micToggle = button({
      type: "button",
      onclick: () => { state.micEnabled.val = !state.micEnabled.val; },
      style: "min-width:72px;min-height:72px;border-radius:50%;border:none;cursor:pointer;font-size:.875rem;margin:4px;",
      "aria-label": "Basculer le microphone",
    });
    Van.derive(() => {
      micToggle.textContent = state.micEnabled.val ? "Micro ON" : "Micro OFF";
      micToggle.style.background = state.micEnabled.val ? "#22c55e" : "#ef4444";
      micToggle.style.color = "#fff";
    });

    const joinBtn = button({
      type: "button",
      "aria-label": "Rejoindre la salle",
      style:
        "min-width:160px;min-height:48px;background:#4f46e5;color:#fff;border:none;border-radius:8px;font-size:1rem;cursor:pointer;padding:12px 24px;margin-top:8px;",
      onclick: () => joinRoom(),
    });
    joinBtn.textContent = "Rejoindre";

    startLocalPreview();

    return div(
      {
        style:
          "max-width:540px;margin:40px auto;padding:24px;font-family:system-ui,sans-serif;",
      },
      h1(
        { style: "font-size:1.25rem;margin-bottom:16px;" },
        escapeHtml(cfg.salonName) + " — Visioconférence"
      ),
      div({ style: "margin-bottom:16px;" }, videoEl),
      div(
        { style: "margin-bottom:16px;" },
        label({ style: "display:block;font-size:.875rem;margin-bottom:4px;" }, "Nom affiché"),
        nameInput
      ),
      div({ style: "margin-bottom:16px;" }, camToggle, " ", micToggle),
      DeviceSelect("Caméra", state.selectedCamera, "cameras"),
      DeviceSelect("Microphone", state.selectedMic, "mics"),
      DeviceSelect("Haut-parleur", state.selectedSpeaker, "speakers"),
      joinBtn
    );
  }

  /* ------------------------------------------------------------------ */
  /* 6. Rejoindre la salle                                               */
  /* ------------------------------------------------------------------ */
  async function joinRoom() {
    if (localVideoTrack) localVideoTrack.stop();
    if (localAudioTrack) localAudioTrack.stop();
    state.localVideoEl = null;

    const room = new Room({
      audioCaptureDefaults: {
        deviceId: state.selectedMic.val || undefined,
      },
      videoCaptureDefaults: {
        deviceId: state.selectedCamera.val || undefined,
      },
    });
    state.room = room;

    /* Événements LiveKit -> état réactif */
    function syncParticipants() {
      const list = [];
      for (const [, p] of room.participants) {
        // Filtre défensif : le bot de transcription ne doit jamais apparaître
        // dans la grille ou le panneau participants (il rejoint déjà en mode
        // hidden, mais ce filtre ceinture-bretelles le retire si visible).
        if (p.identity === "transcriber-bot") continue;
        list.push({
          identity: p.identity,
          name: p.name || p.identity,
          audioMuted: p.isMicrophoneEnabled === false,
          videoMuted: p.isCameraEnabled === false,
          isSpeaking: p.isSpeaking,
          isRemote: true,
        });
      }
      state.participants.val = list;
    }

    room
      .on(RoomEvent.ParticipantConnected, syncParticipants)
      .on(RoomEvent.ParticipantDisconnected, syncParticipants)
      .on(RoomEvent.TrackSubscribed, (track, pub, participant) => {
        syncParticipants();
        attachRemoteTrack(track, participant.identity);
      })
      .on(RoomEvent.TrackUnsubscribed, (track, pub, participant) => {
        syncParticipants();
        detachTrack(track, participant.identity);
      })
      .on(RoomEvent.ActiveSpeakersChanged, (speakers) => {
        highlightSpeakers(speakers.map((s) => s.identity));
      })
      .on(RoomEvent.DataReceived, (data, participant) => {
        const msg = new TextDecoder().decode(data);
        state.chatMessages.val = [
          ...state.chatMessages.val,
          { from: participant?.name || participant?.identity || "?", text: msg },
        ];
      })
      .on(RoomEvent.Disconnected, () => {
        window.location.href = "/salon/" + cfg.slug;
      });

    await room.connect(cfg.url, cfg.token, {
      autoSubscribe: true,
    });

    if (state.micEnabled.val) {
      await room.localParticipant.setMicrophoneEnabled(true);
    }
    if (state.camEnabled.val) {
      await room.localParticipant.setCameraEnabled(true);
    }

    state.phase.val = "room";
  }

  /* ------------------------------------------------------------------ */
  /* 7. Grille des pistes vidéo distantes                                */
  /* ------------------------------------------------------------------ */
  const videoContainers = {}; // identity -> div

  function attachRemoteTrack(track, identity) {
    if (track.kind !== Track.Kind.Video && track.kind !== Track.Kind.Audio) return;
    let container = videoContainers[identity];
    if (!container) {
      container = document.createElement("div");
      container.id = "tile-" + identity;
      container.style.cssText =
        "position:relative;background:#222;border-radius:8px;overflow:hidden;aspect-ratio:16/9;";
      container.dataset.identity = identity;
      const lbl = document.createElement("span");
      lbl.style.cssText =
        "position:absolute;bottom:4px;left:8px;color:#fff;font-size:.75rem;background:rgba(0,0,0,.5);padding:2px 6px;border-radius:4px;";
      lbl.textContent = identity;
      container.appendChild(lbl);
      videoContainers[identity] = container;
      const grid = document.getElementById("visio-grid");
      if (grid) grid.appendChild(container);
    }
    if (track.kind === Track.Kind.Video) {
      const el = document.createElement("video");
      el.autoplay = true;
      el.playsInline = true;
      el.style.cssText = "width:100%;height:100%;object-fit:cover;";
      track.attach(el);
      container.prepend(el);
    } else {
      const el = document.createElement("audio");
      el.autoplay = true;
      track.attach(el);
      container.appendChild(el);
    }
  }

  function detachTrack(track, identity) {
    track.detach();
    if (!videoContainers[identity]) return;
    const room = state.room;
    const p = room && room.participants.get(identity);
    if (!p || (!p.isCameraEnabled)) {
      const container = videoContainers[identity];
      const vids = container.querySelectorAll("video");
      vids.forEach((v) => v.remove());
    }
  }

  function highlightSpeakers(identities) {
    Object.keys(videoContainers).forEach((id) => {
      const el = videoContainers[id];
      el.style.outline = identities.includes(id) ? "2px solid #4f46e5" : "none";
    });
  }

  /* ------------------------------------------------------------------ */
  /* 8. Vue salle                                                        */
  /* ------------------------------------------------------------------ */
  function RoomView() {
    /* Barre de contrôles — 6 boutons */
    function ControlBtn(label_, stateKey, onclickFn, activeColor) {
      const btn = button({
        type: "button",
        onclick: onclickFn,
        style:
          "min-width:72px;min-height:72px;border-radius:50%;border:none;cursor:pointer;font-size:.75rem;margin:4px;color:#fff;",
        "aria-label": label_,
      });
      Van.derive(() => {
        const active = stateKey ? stateKey.val : true;
        btn.textContent = label_;
        btn.style.background = active ? (activeColor || "#4f46e5") : "#6b7280";
      });
      return btn;
    }

    const btnMic = ControlBtn("Micro", state.micEnabled, async () => {
      const room = state.room;
      if (!room) return;
      const next = !state.micEnabled.val;
      await room.localParticipant.setMicrophoneEnabled(next);
      state.micEnabled.val = next;
    }, "#22c55e");

    const btnCam = ControlBtn("Caméra", state.camEnabled, async () => {
      const room = state.room;
      if (!room) return;
      const next = !state.camEnabled.val;
      await room.localParticipant.setCameraEnabled(next);
      state.camEnabled.val = next;
    }, "#22c55e");

    const btnScreen = ControlBtn("Écran", state.screenSharing, async () => {
      const room = state.room;
      if (!room) return;
      const next = !state.screenSharing.val;
      try {
        await room.localParticipant.setScreenShareEnabled(next);
        state.screenSharing.val = next;
      } catch (_) {}
    }, "#f59e0b");

    const btnHand = ControlBtn("Main levée", state.handRaised, () => {
      const room = state.room;
      if (!room) return;
      state.handRaised.val = !state.handRaised.val;
      // Broadcast via data channel : les autres participants voient "✋ identity"
      const msg = state.handRaised.val ? "✋" : "";
      room.localParticipant.publishData(
        new TextEncoder().encode(msg),
        { reliable: true }
      );
    }, "#f59e0b");

    const btnChat = ControlBtn("Chat", state.chatOpen, () => {
      state.chatOpen.val = !state.chatOpen.val;
    }, "#06b6d4");

    const btnHangup = button({
      type: "button",
      style:
        "min-width:72px;min-height:72px;border-radius:50%;border:none;cursor:pointer;font-size:.75rem;margin:4px;background:#ef4444;color:#fff;",
      onclick: () => {
        if (state.room) state.room.disconnect();
        window.location.href = "/salon/" + cfg.slug;
      },
      "aria-label": "Raccrocher",
    });
    btnHangup.textContent = "Raccrocher";

    /* Grille principale */
    const grid = div({
      id: "visio-grid",
      style:
        "display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:12px;flex:1;padding:12px;",
    });

    /* Panneau participants */
    const panParticipants = div({
      style:
        "width:200px;min-width:160px;background:#f8f8f8;border-left:1px solid #e5e7eb;padding:12px;overflow-y:auto;font-size:.875rem;",
    });
    h2({ style: "font-size:.875rem;font-weight:600;margin:0 0 8px;" });
    Van.derive(() => {
      panParticipants.innerHTML = "";
      const ttl = document.createElement("h2");
      ttl.textContent = "Participants";
      ttl.style.cssText = "font-size:.875rem;font-weight:600;margin:0 0 8px;";
      panParticipants.appendChild(ttl);
      state.participants.val.forEach((p) => {
        const row = document.createElement("div");
        row.style.cssText =
          "display:flex;align-items:center;gap:6px;padding:4px 0;border-bottom:1px solid #e5e7eb;";
        const name = document.createElement("span");
        name.style.flex = "1";
        name.textContent = p.name;
        row.appendChild(name);
        if (p.audioMuted) {
          const s = document.createElement("span");
          s.textContent = "Micro coupé";
          s.style.cssText = "font-size:.7rem;color:#ef4444;";
          row.appendChild(s);
        }
        /* Contrôles hôte */
        if (cfg.isOwner) {
          const btnMute = document.createElement("button");
          btnMute.textContent = "Couper";
          btnMute.style.cssText =
            "font-size:.7rem;padding:2px 6px;background:#f59e0b;color:#fff;border:none;border-radius:4px;cursor:pointer;";
          btnMute.onclick = () =>
            post("/salon/" + cfg.slug + "/visio/mute_participant", {
              target_identity: p.identity,
            });
          row.appendChild(btnMute);

          const btnRemove = document.createElement("button");
          btnRemove.textContent = "Expulser";
          btnRemove.style.cssText =
            "font-size:.7rem;padding:2px 6px;background:#ef4444;color:#fff;border:none;border-radius:4px;cursor:pointer;";
          btnRemove.onclick = () =>
            post("/salon/" + cfg.slug + "/visio/remove_participant", {
              target_identity: p.identity,
            });
          row.appendChild(btnRemove);
        }
        panParticipants.appendChild(row);
      });
    });

    /* Panneau chat (data channel LiveKit) */
    const chatPanel = div({
      style:
        "width:260px;background:#fff;border-left:1px solid #e5e7eb;display:flex;flex-direction:column;font-size:.875rem;",
    });
    const chatMsgs = div({
      style: "flex:1;overflow-y:auto;padding:8px;",
    });
    Van.derive(() => {
      chatMsgs.innerHTML = "";
      state.chatMessages.val.forEach((m) => {
        const row = document.createElement("div");
        row.style.cssText = "margin-bottom:6px;";
        row.innerHTML =
          "<strong>" + escapeHtml(m.from) + "</strong> : " + escapeHtml(m.text);
        chatMsgs.appendChild(row);
      });
      chatMsgs.scrollTop = chatMsgs.scrollHeight;
    });
    const chatInputEl = input({
      type: "text",
      placeholder: "Message…",
      style: "flex:1;padding:8px;border:1px solid #e5e7eb;border-radius:0;",
      "aria-label": "Message de chat",
      onkeydown: (e) => {
        if (e.key === "Enter" && state.room) {
          const msg = e.target.value.trim();
          if (!msg) return;
          state.room.localParticipant.publishData(
            new TextEncoder().encode(msg),
            { reliable: true }
          );
          state.chatMessages.val = [
            ...state.chatMessages.val,
            { from: "Moi", text: msg },
          ];
          e.target.value = "";
        }
      },
    });
    add(
      chatPanel,
      div(
        {
          style:
            "padding:8px;background:#f8f8f8;border-bottom:1px solid #e5e7eb;font-weight:600;",
        },
        "Chat"
      ),
      chatMsgs,
      div(
        { style: "display:flex;border-top:1px solid #e5e7eb;" },
        chatInputEl
      )
    );

    /* Bouton Terminer la salle (hôte seulement) */
    const endRoomBtn = cfg.isOwner
      ? button({
          type: "button",
          style:
            "min-width:120px;min-height:72px;background:#7f1d1d;color:#fff;border:none;border-radius:8px;font-size:.875rem;cursor:pointer;margin:0 8px;",
          onclick: () =>
            post("/salon/" + cfg.slug + "/visio/end", {}).then(() => {
              window.location.href = "/salon/" + cfg.slug;
            }),
          "aria-label": "Terminer la salle pour tous",
        })
      : null;
    if (endRoomBtn) endRoomBtn.textContent = "Terminer la salle";

    /* Layout global */
    const chatWrap = div({ style: "display:flex;height:100%;" });
    Van.derive(() => {
      chatWrap.innerHTML = "";
      if (state.chatOpen.val) {
        chatWrap.appendChild(chatPanel);
      }
    });

    return div(
      {
        id: "visio-room",
        style:
          "display:flex;flex-direction:column;height:100vh;font-family:system-ui,sans-serif;background:#18181b;color:#fff;",
      },
      /* Barre supérieure */
      div(
        {
          style:
            "display:flex;align-items:center;padding:8px 16px;background:#09090b;gap:12px;border-bottom:1px solid #27272a;",
        },
        span(
          { style: "font-size:1rem;font-weight:600;" },
          escapeHtml(cfg.salonName)
        ),
        endRoomBtn ? endRoomBtn : span("")
      ),
      /* Zone principale */
      div(
        { style: "display:flex;flex:1;overflow:hidden;" },
        grid,
        panParticipants,
        chatWrap
      ),
      /* Barre de contrôles */
      div(
        {
          style:
            "display:flex;justify-content:center;align-items:center;padding:12px;background:#09090b;border-top:1px solid #27272a;flex-wrap:wrap;gap:4px;",
        },
        btnMic,
        btnCam,
        btnScreen,
        btnHand,
        btnChat,
        btnHangup
      )
    );
  }

  /* ------------------------------------------------------------------ */
  /* 9. Rendu racine — bascule réactive sur phase                        */
  /* ------------------------------------------------------------------ */
  const root = document.getElementById("visio-root");
  if (!root) {
    document.body.innerHTML = "<p>Erreur : élément racine #visio-root absent.</p>";
    return;
  }

  let currentPhase = null;
  Van.derive(() => {
    const phase = state.phase.val;
    if (phase === currentPhase) return;
    currentPhase = phase;
    root.innerHTML = "";
    if (phase === "prejoin") {
      add(root, PrejoinView());
    } else {
      add(root, RoomView());
    }
  });
})();
