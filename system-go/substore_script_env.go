package main

// subStoreScriptEnvShim installs the client environment the embedded core
// looks for, BEFORE the core is evaluated.
//
// This ordering is not incidental. The core decides once, at module-init
// time, which client it is running inside — `var isSurge = typeof $httpClient
// !== 'undefined' && !isLoon` and friends are evaluated as the bundle loads,
// and the singleton it constructs captures the result. Install these after
// the core and nothing changes; install them before and every HTTP path in
// the core (user scripts' $.http, the DoH resolver behind Resolve Domain,
// remote produceArtifact downloads) resolves to the one function below.
//
// Presenting as Surge rather than Node is deliberate: the Node branch calls
// eval("require('undici')") and reaches for process.env, neither of which
// exists here, while the Surge branch is a plain callback API this shim can
// satisfy exactly. The blast radius was checked against the upstream tree
// before choosing it — no producer branches on the client (the only env
// branches outside the HTTP layer add proxy fields when a proxy node is set,
// which we never set, and choose the Loon-only script injection list, which
// stays off). The visible difference is $.env.isSurge, which if anything
// helps: community scripts that special-case Surge get a working client.
//
// Storage and notification are stubbed in memory rather than left undefined,
// because the core's OpenAPI constructor reads its cache during init and the
// Surge branch would otherwise dereference an undefined $persistentStore
// while the bundle is still loading. In-memory matches today's effective
// behavior (scriptResourceCache already lives and dies with the runtime);
// what changes is that it no longer throws on the way there.
const subStoreScriptEnvShim = `
(function () {
  var B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  function b64ToBytes(input) {
    var clean = String(input || "").replace(/[^A-Za-z0-9+/]/g, "");
    var out = new Uint8Array(Math.floor((clean.length * 3) / 4));
    var o = 0, buffer = 0, bits = 0;
    for (var i = 0; i < clean.length; i++) {
      buffer = (buffer << 6) | B64.indexOf(clean.charAt(i));
      bits += 6;
      if (bits >= 8) {
        bits -= 8;
        out[o++] = (buffer >> bits) & 255;
      }
    }
    return out.subarray(0, o);
  }

  function normalize(options) {
    return typeof options === "string" ? { url: options } : (options || {});
  }

  function request(method, options, callback) {
    var opts = normalize(options);
    // We declared ourselves Surge, so the core has already converted the
    // caller's milliseconds into whole seconds. Convert back rather than
    // guessing: a script that asked for 5000 ms means 5000 ms.
    var timeoutMs = 0;
    if (typeof opts.timeout === "number" && isFinite(opts.timeout)) {
      timeoutMs = Math.round(opts.timeout * 1000);
    }
    var binary = opts["binary-mode"] === true || opts.encoding === null;
    var payload = {
      method: method,
      url: opts.url,
      headers: opts.headers || opts.header || undefined,
      timeout_ms: timeoutMs,
      binary: binary,
    };
    if (typeof opts.body === "string") {
      payload.body = opts.body;
    } else if (opts.body != null && typeof opts.body === "object") {
      payload.body = JSON.stringify(opts.body);
    }

    var answer;
    try {
      answer = JSON.parse(__lattice_host_http(JSON.stringify(payload)));
    } catch (failure) {
      // The Go side throws with a sentence a script author can act on
      // (budget exhausted, url must be http or https, request failed).
      callback(failure && failure.message ? failure.message : String(failure), null, null);
      return;
    }
    var body = answer.body_base64 != null ? b64ToBytes(answer.body_base64) : (answer.body || "");
    callback(null, { status: answer.status, statusCode: answer.status, headers: answer.headers || {} }, body);
  }

  var client = {};
  ["get", "post", "put", "delete", "head", "options", "patch"].forEach(function (method) {
    client[method] = function (options, callback) {
      request(method.toUpperCase(), options, callback || function () {});
    };
  });
  globalThis.$httpClient = client;

  // Surge-shaped storage, backed by this runtime only. Nothing here is a
  // durable store, and nothing pretends to be: a script that writes a cache
  // entry gets it back within the same call and not after.
  var memory = {};
  globalThis.$persistentStore = {
    read: function (key) {
      var value = memory[key == null ? "" : String(key)];
      return value === undefined ? null : value;
    },
    write: function (data, key) {
      memory[key == null ? "" : String(key)] = data;
      return true;
    },
  };

  globalThis.$notification = { post: function () {} };
  // Only read when a script pins a proxy node, which this deployment never
  // does; present so the lookup is a miss rather than a TypeError.
  globalThis.$environment = { "surge-build": "0", "surge-version": "0" };
})();
`
