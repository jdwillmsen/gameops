// Stand-in for the fleet: one process answers as every unit, told apart by the
// loopback address crew-control dials (127.0.0.N). State changes every poll
// and each unit flips a task on and off, so the fleet event stream carries
// about one event per unit per second, the worst steady rate the poller can
// produce.
import http from 'node:http';

export const unitHost = n => `127.0.0.${n + 1}`;

function stateFor(n, now) {
  const tick = Math.floor(now / 1000);
  const working = (tick + n) % 2 === 0;
  const phase = (tick + n * 7) % 40;
  return {
    state: {
      dead: false,
      health: 20 - (phase % 5),
      health_known: true,
      hunger: 20 - (phase % 7),
      hunger_known: true,
      position: [n * 10 + phase, 64, n * 5 - phase],
      position_known: true,
      yaw: (phase * 9) % 360,
      dimension: 0,
      inventory: [],
      effects: [],
      unique_id: `unit-${n}`,
    },
    tasks: {
      current: working ? { id: `t-${n}-${tick}`, template: 'gather', state: 'running', item: 'oak_log', qty: 64, progress: phase } : null,
      recent: working ? [] : [{ id: `t-${n}-${tick - 1}`, state: 'done', reason: '', created: new Date(now - 2000).toISOString(), updated: new Date(now - 1000).toISOString() }],
    },
  };
}

// Camera stub: a canvas that redraws every frame. The real page is WebGL,
// whose cost depends on the GPU, so this holds the per-feed work constant
// and the run measures what the console itself adds on top.
const cameraPage = (cost) => `<!doctype html><meta charset=utf-8><body style="margin:0;background:#111"><canvas id=c width=320 height=180 style="width:100%;height:100%"></canvas><script>
const c=document.getElementById('c').getContext('2d');let t=0;
(function f(){t++;c.fillStyle='#223';c.fillRect(0,0,320,180);
for(let i=0;i<${cost};i++){c.fillStyle='hsl('+((i*7+t)%360)+',50%,50%)';c.fillRect((i*13+t)%300,(i*29)%160,16,16);}
requestAnimationFrame(f);})();</script>`;

export function startMock({ units = 20, cameraCost = 150 } = {}) {
  const server = http.createServer((req, res) => {
    const n = Number(String(req.headers.host || '').match(/^127\.0\.0\.(\d+)/)?.[1]) - 1;
    const path = req.url.split('?')[0];
    if (path === '/v1/state') {
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify(stateFor(n, Date.now())));
    } else if (path === '/camera') {
      res.setHeader('content-type', 'text/html');
      res.end(cameraPage(cameraCost));
    } else {
      res.statusCode = 404;
      res.end();
    }
  });
  return new Promise(resolve => server.listen(0, '0.0.0.0', () => {
    const port = server.address().port;
    const list = Array.from({ length: units }, (_, i) => ({
      id: `crew-${String(i + 1).padStart(2, '0')}`,
      name: `Unit ${i + 1}`,
      url: `http://${unitHost(i)}:${port}`,
    }));
    resolve({ port, units: list, close: () => server.close() });
  }));
}
