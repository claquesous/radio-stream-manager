# One ClaqRadio stream, spawned by the stream manager with its config in env
# vars. Like ices + ClaqRadio.pm, it asks the backend for every song
# (POST /private/streams/:id/plays), which also records the Play. On startup it
# first asks what is already on the air (GET .../plays/current) and joins that
# song at its offset, so a restart doesn't skip to a new song.

settings.log.stdout := true
settings.log.file := false
# ffmpeg writes straight to stderr, which the stream manager logs as errors,
# and it warns about every song ("Estimating duration from bitrate").
settings.ffmpeg.log.verbosity := "error"

stream_id = environment.get("STREAM_ID")
api = environment.get("CLAQRADIO_STREAM_URL", default="http://localhost:3000")
premium = environment.get("PREMIUM") == "true"

# Now-playing JSON for recent plays, keyed by play id. It goes out as the ICY
# StreamTitle, which the frontend player JSON.parses.
now_playing = ref([])

# Turns a play from the backend into a request for its song, remembering its
# now-playing JSON for icy_song. Null if the response doesn't parse.
def play_request(resp) =
  try
    let json.parse (play :
      {
        id: int,
        offset: float?,
        artist: {id: int, name: string},
        song: {id: int, title: string, path: string, album: {title: string}?}
      }
    ) = resp
    meta = json.stringify(compact=true, {
      id = play.id,
      artist = play.artist,
      song = {id = play.song.id, title = play.song.title, album = play.song.album}
    })
    now_playing := list.prefix(5, (string(play.id), meta) :: now_playing())
    # offset (optional): seconds into the song to start at.
    offset = play.offset ?? 0.
    cue_in = if offset > 0. then ",liq_cue_in=\"#{offset}\"" else "" end
    request.create(
      "annotate:claqradio_play=\"#{play.id}\"#{cue_in}:#{play.song.path}"
    )
  catch err do
    log.important("Bad plays response: #{err}")
    null
  end
end

# Picks the next song, recording a Play.
def next_song() =
  resp = http.post("#{api}/private/streams/#{stream_id}/plays")
  if resp.status_code != 201 then
    log.important("POST plays returned #{resp.status_code}: #{resp}")
    null
  else
    play_request(resp)
  end
end

# The song on the air now, with how far in it is, so a restart picks it back
# up instead of recording (and tooting) a new Play. Read-only; 404 when
# nothing is playing or the song is about to end.
def current_song() =
  resp = http.get("#{api}/private/streams/#{stream_id}/plays/current")
  if resp.status_code == 200 then
    play_request(resp)
  else
    if resp.status_code != 404 then
      log.important("GET plays/current returned #{resp.status_code}: #{resp}")
    end
    null
  end
end

# icy_song only sees exported metadata, and the default export list drops
# custom annotations.
settings.encoder.metadata.export := [
  "claqradio_play", ...settings.encoder.metadata.export()
]

def icy_song(m) =
  list.assoc.nullable(m["claqradio_play"], now_playing())
end

# Every POST records a Play, so ask for the next song only as the current one
# is about to end, like ices does, so a Play's playtime is (nearly) when it
# actually started. request.dynamic can't do that: it always keeps one request
# queued ahead.
queue = request.queue()

# A failed fetch is retried, but the POST has usually recorded (and tooted) a
# Play already, so back off from 5s to a minute: an unreadable music mount
# then costs about one Play a minute, not twelve.
retry_delay = ref(5.)

# Only the first fetch after startup resumes the current song; every later one
# (including a retry, if that song couldn't be resolved) starts a new one.
resuming = ref(true)

def first_song() =
  r = current_song()
  if null.defined(r) then r else next_song() end
end

def rec fetch() =
  def retry() =
    thread.run(delay=retry_delay(), fetch)
    retry_delay := min(retry_delay() * 2., 60.)
  end
  def enqueue(r) =
    if request.resolve(r) then
      retry_delay := 5.
      queue.push(r)
    else
      log.important("Could not resolve #{request.uri(r)}")
      request.destroy(r)
      retry()
    end
  end
  song =
    if resuming() then
      resuming := false
      first_song()
    else
      next_song()
    end
  null.case(song, retry, enqueue)
end

queue.on_position(
  synchronous=false, position=5., remaining=true, allow_partial=true,
  fun (_, _) -> fetch()
)
thread.run(fetch)

radio = mksafe(queue)

def stream(format) =
  output.icecast(
    format,
    host=environment.get("ICECAST_HOST", default="localhost"),
    port=int_of_string(environment.get("ICECAST_PORT", default="8000")),
    password=environment.get("ICECAST_PASSWORD"),
    mount="/stream-#{stream_id}",
    name=environment.get("STREAM_NAME"),
    genre=environment.get("STREAM_GENRE"),
    description=environment.get("STREAM_DESCRIPTION"),
    url=environment.get("STREAM_URL"),
    public=false,
    icy_metadata=["song"],
    icy_song=icy_song,
    radio
  )
end

# Same output as ices: LAME resamples a 64k stream to 24kHz, where Liquidsoap
# would otherwise stay at 44.1kHz.
if premium then
  stream(%mp3(bitrate=128))
else
  stream(%mp3(bitrate=64, samplerate=24000))
end
