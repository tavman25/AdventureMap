/* ============================================================
   TRAVEL MAP — App JavaScript
   Leaflet map, CRUD pins, Google Maps import
   ============================================================ */

// ─── State ───────────────────────────────────────────────────
const state = {
  pins:         [],      // all pin objects from the server
  markers:      {},      // id → Leaflet marker
  activeId:     null,    // currently selected pin id
  addClickMode: false,   // whether next map click sets coords
  editingId:    null,    // pin being edited (null = new)
  pendingPickDraft: null,
  pinImageList: [],
  draggedImageIndex: null,
  countryCache: null,
  countryLookupInFlight: {},
  statsRunId: 0,
  lightboxImages: [],
  lightboxImageTitles: [],
  lightboxIndex: 0,
  lightboxTitleBase: 'Photo',
  slideshowActive: false,
  slideshowTimerId: null,
  slideshowDelayMs: 5000,
  authEnabled: false,
  isAdmin: false,
  passwordLoginEnabled: false,
  googleLoginEnabled: false,
  googleClientId: '',
  googleScriptLoaded: false,
  defaultTitle: '',
  profileTitle: '',
  routeConnections: [],
  routeConnectMode: false,
  routeDragStartId: null,
  routeGhostLine: null,
};

state.defaultTitle = document.querySelector('.logo-text')?.textContent || 'Travel Map';

const COUNTRY_CACHE_KEY = 'travel-map-country-cache-v1';

function loadCountryCache() {
  try {
    const raw = localStorage.getItem(COUNTRY_CACHE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function saveCountryCache() {
  try {
    localStorage.setItem(COUNTRY_CACHE_KEY, JSON.stringify(state.countryCache));
  } catch {
    // Ignore quota/storage errors.
  }
}

state.countryCache = loadCountryCache();

function getPinCountryCacheKey(pin) {
  const lat = Number(pin.latitude).toFixed(4);
  const lng = Number(pin.longitude).toFixed(4);
  return `${lat},${lng}`;
}

function getCachedCountryCode(pin) {
  const key = getPinCountryCacheKey(pin);
  return state.countryCache[key] || '';
}

function setCachedCountryCode(pin, countryCode) {
  const key = getPinCountryCacheKey(pin);
  state.countryCache[key] = countryCode;
  saveCountryCache();
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function resolveCountryCode(pin) {
  const cached = getCachedCountryCode(pin);
  if (cached) return cached;

  const key = getPinCountryCacheKey(pin);
  if (state.countryLookupInFlight[key]) {
    return state.countryLookupInFlight[key];
  }

  const lookupPromise = (async () => {
    try {
      const url = `https://nominatim.openstreetmap.org/reverse?format=jsonv2&zoom=3&addressdetails=1&lat=${encodeURIComponent(pin.latitude)}&lon=${encodeURIComponent(pin.longitude)}`;
      const res = await fetch(url, {
        headers: {
          Accept: 'application/json',
        },
      });
      if (!res.ok) return '';
      const data = await res.json();
      const code = String(data?.address?.country_code || '').toUpperCase();
      if (code) {
        setCachedCountryCode(pin, code);
      }
      return code;
    } catch {
      return '';
    } finally {
      delete state.countryLookupInFlight[key];
    }
  })();

  state.countryLookupInFlight[key] = lookupPromise;
  return lookupPromise;
}

function getViewportMinZoom() {
  const worldPixelWidthAtZoom0 = 256;
  const viewportWidth = Math.max(window.innerWidth, 1024);
  const calculated = Math.ceil(Math.log2(viewportWidth / worldPixelWidthAtZoom0));
  return Math.max(2, Math.min(calculated, 5));
}

// ─── Map initialisation ──────────────────────────────────────
const viewportMinZoom = getViewportMinZoom();
const map = L.map('map', {
  center: [20, 0],
  zoom: viewportMinZoom,
  minZoom: viewportMinZoom,
  maxZoom: 19,
  zoomControl: true,
  attributionControl: true,
  worldCopyJump: false,
  maxBounds: [[-90, -180], [90, 180]],
  maxBoundsViscosity: 1.0,
});

// Dark base tile layer (CartoDB Dark)
L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
  attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors &copy; <a href="https://carto.com/">CARTO</a>',
  subdomains: 'abcd',
  maxZoom: 19,
  noWrap: true,
  bounds: [[-90, -180], [90, 180]],
}).addTo(map);

window.addEventListener('resize', () => {
  const nextMinZoom = getViewportMinZoom();
  map.setMinZoom(nextMinZoom);
  if (map.getZoom() < nextMinZoom) {
    map.setZoom(nextMinZoom);
  }
});

// MarkerCluster group
const clusterGroup = L.markerClusterGroup({
  showCoverageOnHover: false,
  maxClusterRadius: 50,
  spiderfyOnMaxZoom: true,
  disableClusteringAtZoom: 10,
});
map.addLayer(clusterGroup);

const routeLayerGroup = L.layerGroup().addTo(map);

function getPinById(pinID) {
  return state.pins.find((pin) => pin.id === pinID) || null;
}

function buildRoutePolylinePoints(route) {
  const fromPin = getPinById(route.from_pin_id);
  const toPin = getPinById(route.to_pin_id);
  if (!fromPin || !toPin) return null;
  return [
    [Number(fromPin.latitude), Number(fromPin.longitude)],
    [Number(toPin.latitude), Number(toPin.longitude)],
  ];
}

function renderRouteConnections() {
  routeLayerGroup.clearLayers();

  state.routeConnections.forEach((route, idx) => {
    const points = buildRoutePolylinePoints(route);
    if (!points) return;
    const fromPin = getPinById(route.from_pin_id);
    const toPin = getPinById(route.to_pin_id);

    const line = L.polyline(points, {
      color: '#ffd166',
      weight: 3,
      opacity: 0.9,
      dashArray: '10 6',
    });

    line.bindTooltip(`Leg ${idx + 1}: ${fromPin?.title || route.from_pin_id} -> ${toPin?.title || route.to_pin_id}`, {
      direction: 'center',
      sticky: true,
    });

    if (state.isAdmin && state.routeConnectMode) {
      line.on('click', async () => {
        if (!confirm('Remove this route segment?')) return;
        try {
          await deleteRoute(route.id);
          await loadRoutes();
          showToast('Route segment removed', 'success');
        } catch (err) {
          showToast(err.message, 'error');
        }
      });
    }

    routeLayerGroup.addLayer(line);
  });
}

function ensureRouteGhostLine() {
  if (state.routeGhostLine) return state.routeGhostLine;
  state.routeGhostLine = L.polyline([], {
    color: '#fca311',
    weight: 2,
    opacity: 0.85,
    dashArray: '5 5',
    interactive: false,
  }).addTo(map);
  return state.routeGhostLine;
}

function clearRouteGhostLine() {
  if (!state.routeGhostLine) return;
  state.routeGhostLine.setLatLngs([]);
}

function capturePinFormDraft() {
  return {
    editingId: state.editingId,
    title: document.getElementById('pinTitle').value,
    description: document.getElementById('pinDescription').value,
    imageUrl: document.getElementById('pinImageUrl').value,
    imageList: [...state.pinImageList],
    color: document.getElementById('pinColor').value,
    icon: document.getElementById('pinIcon').value,
    visitedAt: document.getElementById('pinVisitedAt').value,
  };
}

function restorePinFormDraftWithCoords(draft, lat, lng) {
  state.editingId = draft?.editingId || null;
  document.getElementById('modalTitle').textContent = state.editingId ? '✏️ Edit Pin' : '📍 New Pin';
  document.getElementById('pinTitle').value = draft?.title || '';
  document.getElementById('pinDescription').value = draft?.description || '';
  document.getElementById('pinImageUrl').value = draft?.imageUrl || '';
  state.pinImageList = Array.isArray(draft?.imageList) ? [...draft.imageList] : [];
  renderPinImageListEditor();
  document.getElementById('pinColor').value = draft?.color || '#FF5722';
  document.getElementById('pinIcon').value = draft?.icon || '📍';
  document.getElementById('pinVisitedAt').value = draft?.visitedAt || '';
  document.getElementById('pinLat').value = lat.toFixed(6);
  document.getElementById('pinLng').value = lng.toFixed(6);
  document.getElementById('coordHint').textContent = `✅ Coordinates set: ${lat.toFixed(4)}, ${lng.toFixed(4)}`;
}

// Map click handler (for placing pins)
map.on('click', (e) => {
  if (state.addClickMode) {
    const draft = state.pendingPickDraft;
    restorePinFormDraftWithCoords(draft, e.latlng.lat, e.latlng.lng);
    state.addClickMode = false;
    state.pendingPickDraft = null;
    map.getContainer().style.cursor = '';
    openModal('pinModal');
    showToast('Coordinates picked! Fill in the form and save.', 'success');
  }
});

map.on('mousemove', (e) => {
  if (!state.routeConnectMode || !state.routeDragStartId) return;
  const startPin = getPinById(state.routeDragStartId);
  if (!startPin) {
    state.routeDragStartId = null;
    clearRouteGhostLine();
    return;
  }
  const ghost = ensureRouteGhostLine();
  ghost.setLatLngs([
    [Number(startPin.latitude), Number(startPin.longitude)],
    [e.latlng.lat, e.latlng.lng],
  ]);
});

map.on('mouseup', () => {
  // Delay cancellation so marker mouseup can complete a valid connection first.
  setTimeout(() => {
    if (!state.routeDragStartId) return;
    state.routeDragStartId = null;
    clearRouteGhostLine();
  }, 0);
});

document.getElementById('coordHint').addEventListener('click', () => {
  state.pendingPickDraft = capturePinFormDraft();
  state.addClickMode = true;
  map.getContainer().style.cursor = 'crosshair';
  closePinModal(true);
  showToast('Click anywhere on the map to set coordinates');
});

// ─── Pin Marker Helpers ──────────────────────────────────────
function createPinIcon(pin) {
  return L.divIcon({
    className: '',
    html: `<div class="custom-pin" style="background:${pin.color || '#FF5722'}">
             <span class="custom-pin-inner">${pin.icon || '📍'}</span>
           </div>`,
    iconSize: [36, 36],
    iconAnchor: [18, 36],
    popupAnchor: [0, -38],
  });
}

function buildPopupHTML(pin) {
  const lat  = parseFloat(pin.latitude).toFixed(4);
  const lng  = parseFloat(pin.longitude).toFixed(4);
  const images = getPinImages(pin);
  const imageURL = images[0] || '';
  const meta = [
    pin.visited_at ? `🗓 ${pin.visited_at}` : '',
    `📌 ${lat}, ${lng}`,
  ].filter(Boolean).join('  ·  ');

  return `
    <div class="popup-content">
      <div class="popup-header">
        <span class="popup-icon">${pin.icon || '📍'}</span>
        <span class="popup-title">${escapeHTML(pin.title)}</span>
      </div>
      ${imageURL ? `<button type="button" class="popup-photo-wrap" onclick="openGalleryForPin(${pin.id})" title="View photo gallery">
        <img class="popup-photo" src="${imageURL}" alt="${escapeHTML(pin.title)}" loading="lazy" referrerpolicy="no-referrer" />
        ${images.length > 1 ? `<span class="popup-photo-count">${images.length} photos</span>` : `<span class="popup-photo-count">Open photo</span>`}
      </button>` : ''}
      ${pin.description ? `<p class="popup-desc">${escapeHTML(pin.description)}</p>` : ''}
      <div class="popup-meta">${meta}</div>
      ${state.isAdmin ? `<div class="popup-actions">
        <button class="btn btn-ghost" onclick="editPinById(${pin.id})">✏️ Edit</button>
        <button class="btn btn-danger" onclick="deletePinById(${pin.id})">🗑 Delete</button>
      </div>` : ''}
    </div>`;
}

function addMarkerToMap(pin) {
  const marker = L.marker([pin.latitude, pin.longitude], { icon: createPinIcon(pin) });
  marker.bindPopup(buildPopupHTML(pin), { maxWidth: 280 });
  marker.on('mousedown', (e) => {
    handleRouteDragStart(pin.id, e.latlng);
  });
  marker.on('mouseup', async (e) => {
    await handleRouteDragEnd(pin.id, e.latlng);
  });
  marker.on('click', () => {
    if (state.routeConnectMode) {
      marker.closePopup();
      return;
    }
    state.activeId = pin.id;
    highlightSidebarCard(pin.id);
  });
  clusterGroup.addLayer(marker);
  state.markers[pin.id] = marker;
}

function removeMarkerFromMap(id) {
  if (state.markers[id]) {
    clusterGroup.removeLayer(state.markers[id]);
    delete state.markers[id];
  }
}

function refreshMarkerPopups() {
  state.pins.forEach((pin) => {
    const marker = state.markers[pin.id];
    if (!marker) return;
    marker.setPopupContent(buildPopupHTML(pin));
  });
}

// ─── API Calls ───────────────────────────────────────────────
async function fetchPins() {
  const res = await fetch('/api/pins');
  if (!res.ok) throw new Error('Failed to load pins');
  return res.json();
}

async function fetchRoutes() {
  const res = await fetch('/api/routes');
  if (!res.ok) throw new Error('Failed to load routes');
  return res.json();
}

async function createRoute(payload) {
  const res = await fetch('/api/routes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to create route');
  return data;
}

async function deleteRoute(routeID) {
  const res = await fetch(`/api/routes/${routeID}`, { method: 'DELETE' });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to delete route');
  return data;
}

async function clearRoutesRequest() {
  const res = await fetch('/api/routes', { method: 'DELETE' });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to clear routes');
  return data;
}

async function createPin(data) {
  const res = await fetch('/api/pins', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) { const e = await res.json(); throw new Error(e.error || 'Failed to create pin'); }
  return res.json();
}

async function updatePin(id, data) {
  const res = await fetch(`/api/pins/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) { const e = await res.json(); throw new Error(e.error || 'Failed to update pin'); }
  return res.json();
}

async function deletePin(id) {
  const res = await fetch(`/api/pins/${id}`, { method: 'DELETE' });
  if (!res.ok) { const e = await res.json(); throw new Error(e.error || 'Failed to delete pin'); }
}

async function createCloneInviteRequest(payload) {
  const res = await fetch('/api/clone/invites', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to create clone invite');
  return data;
}

async function acceptCloneInviteRequest(inviteToken) {
  const res = await fetch('/api/clone/accept', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ invite_token: inviteToken }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to accept clone invite');
  return data;
}

async function fetchProfile() {
  const res = await fetch('/api/profile');
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to load profile');
  return data;
}

async function updateProfileDisplayTitle(displayTitle) {
  const res = await fetch('/api/profile', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ display_title: displayTitle }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to update title');
  return data;
}

function applyMapTitle(title) {
  const logoText = document.querySelector('.logo-text');
  if (!logoText) return;
  const finalTitle = (title || '').trim() || state.defaultTitle;
  logoText.textContent = finalTitle;
}

async function loadProfileTitle() {
  try {
    const profile = await fetchProfile();
    state.profileTitle = String(profile.display_title || '').trim();
    applyMapTitle(state.profileTitle);
  } catch {
    state.profileTitle = '';
    applyMapTitle('');
  }
}

async function fetchCloneInvites() {
  const res = await fetch('/api/clone/invites?limit=50');
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to load clone invites');
  return data.invites || [];
}

async function revokeCloneInviteRequest(inviteId) {
  const res = await fetch(`/api/clone/invites/${inviteId}/revoke`, { method: 'POST' });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to revoke invite');
  return data;
}

async function fetchCloneEvents() {
  const res = await fetch('/api/clone/events?limit=100');
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to load clone activity');
  return data.events || [];
}

// ─── Load & Render ───────────────────────────────────────────
async function loadPins() {
  try {
    state.pins = await fetchPins();
    renderSidebarPinList(state.pins);
    clusterGroup.clearLayers();
    state.markers = {};
    state.pins.forEach(addMarkerToMap);
    renderRouteConnections();
    updateStats();
  } catch (err) {
    clusterGroup.clearLayers();
    state.markers = {};
    state.pins = [];
    renderRouteConnections();
    renderSidebarPinList(state.pins);
    updateStats();
    if (state.authEnabled && /401|Failed to load pins/i.test(err.message)) {
      return;
    }
    showToast('Could not load pins: ' + err.message, 'error');
  }
}

async function loadRoutes() {
  try {
    state.routeConnections = await fetchRoutes();
  } catch (err) {
    if (state.authEnabled && /401|Failed to load routes/i.test(err.message)) {
      state.routeConnections = [];
    } else {
      state.routeConnections = [];
      showToast('Could not load routes: ' + err.message, 'error');
    }
  }
  renderRouteConnections();
  updateStats();
}

function updateRouteModeUI() {
  const routeModeBtn = document.getElementById('routeModeBtn');
  const clearRouteBtn = document.getElementById('clearRouteBtn');
  if (routeModeBtn) {
    routeModeBtn.classList.toggle('active', state.routeConnectMode);
    routeModeBtn.innerHTML = state.routeConnectMode
      ? '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M5 5a3 3 0 1 0 0 6 3 3 0 0 0 0-6zm14 8a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM7.59 9.58l8.83 4.83 1-1.74-8.83-4.83z"/></svg> Connecting...'
      : '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M5 5a3 3 0 1 0 0 6 3 3 0 0 0 0-6zm14 8a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM7.59 9.58l8.83 4.83 1-1.74-8.83-4.83z"/></svg> Route Mode';
  }
  if (clearRouteBtn) {
    clearRouteBtn.style.display = state.routeConnectMode ? '' : 'none';
  }

  // Route drawing uses pointer drag between pins; disable map pan to avoid fighting the gesture.
  if (state.routeConnectMode) {
    map.dragging.disable();
  } else {
    map.dragging.enable();
  }
}

function toggleRouteConnectMode() {
  if (!requireAdminAccess()) return;
  state.routeConnectMode = !state.routeConnectMode;
  state.routeDragStartId = null;
  clearRouteGhostLine();
  updateRouteModeUI();
  renderRouteConnections();

  if (state.routeConnectMode) {
    showToast('Route mode ON: map panning is locked. Drag pin-to-pin to create a travel leg.', 'success');
  } else {
    showToast('Route mode OFF', 'success');
  }
}

function handleRouteDragStart(pinID, latlng) {
  if (!state.routeConnectMode || !state.isAdmin) return;
  state.routeDragStartId = pinID;
  const ghost = ensureRouteGhostLine();
  ghost.setLatLngs([
    [latlng.lat, latlng.lng],
    [latlng.lat, latlng.lng],
  ]);
}

async function handleRouteDragEnd(pinID, latlng) {
  if (!state.routeConnectMode || !state.isAdmin) return;
  const startID = state.routeDragStartId;
  state.routeDragStartId = null;
  clearRouteGhostLine();

  if (!startID || startID === pinID) return;

  const exists = state.routeConnections.some((route) => route.from_pin_id === startID && route.to_pin_id === pinID);
  if (exists) {
    showToast('That route leg already exists', 'error');
    return;
  }

  try {
    await createRoute({ from_pin_id: startID, to_pin_id: pinID });
    await loadRoutes();
    const fromPin = getPinById(startID);
    const toPin = getPinById(pinID);
    showToast(`Connected ${fromPin?.title || 'start'} -> ${toPin?.title || 'end'}`, 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function clearAllRoutes() {
  if (!requireAdminAccess()) return;
  if (!confirm('Clear every connected route segment for your map?')) return;
  try {
    await clearRoutesRequest();
    state.routeConnections = [];
    renderRouteConnections();
    updateStats();
    showToast('All route segments cleared', 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function renderSidebarPinList(pins) {
  const list = document.getElementById('pinList');
  if (!pins.length) {
    const message = state.authEnabled && !state.isAdmin
      ? 'Sign in to view your personal travel map and photos.'
      : 'No places yet!<br>Click <strong>Add Pin</strong> or click anywhere on the map to start your travel story.';
    list.innerHTML = `<div class="empty-state">
      <span class="empty-icon">🌍</span>
      <p>${message}</p>
    </div>`;
    return;
  }
  list.innerHTML = pins.map(p => `
    <div class="pin-card ${state.activeId === p.id ? 'active' : ''}" id="card-${p.id}" onclick="flyToPin(${p.id})">
      <span class="pin-card-icon">${p.icon || '📍'}</span>
      <div class="pin-card-body">
        <div class="pin-card-title">${escapeHTML(p.title)}</div>
        <div class="pin-card-meta">${p.visited_at || `${parseFloat(p.latitude).toFixed(2)}, ${parseFloat(p.longitude).toFixed(2)}`}</div>
      </div>
      ${state.isAdmin ? `<div class="pin-card-actions">
        <button title="Edit" onclick="event.stopPropagation(); editPinById(${p.id})">✏️</button>
        <button title="Delete" class="delete" onclick="event.stopPropagation(); deletePinById(${p.id})">🗑</button>
      </div>` : ''}
    </div>`).join('');
}

function requireAdminAccess() {
  if (!state.authEnabled) return true;
  if (state.isAdmin) return true;
  openModal('authModal');
  showToast('Admin login required for changes', 'error');
  return false;
}

function setVisibleMarkers(pinsToShow) {
  const visibleIds = new Set(pinsToShow.map(p => p.id));
  state.pins.forEach((pin) => {
    const marker = state.markers[pin.id];
    if (!marker) return;
    const shouldShow = visibleIds.has(pin.id);
    const isShown = clusterGroup.hasLayer(marker);
    if (shouldShow && !isShown) clusterGroup.addLayer(marker);
    if (!shouldShow && isShown) clusterGroup.removeLayer(marker);
  });
}

function filterPins(query) {
  const q = query.trim().toLowerCase();
  if (!q) {
    renderSidebarPinList(state.pins);
    setVisibleMarkers(state.pins);
    return;
  }
  const filtered = state.pins.filter(p =>
    p.title.toLowerCase().includes(q) ||
    (p.description || '').toLowerCase().includes(q) ||
    (p.visited_at || '').toLowerCase().includes(q)
  );
  if (!filtered.length) {
    const list = document.getElementById('pinList');
    list.innerHTML = `<div class="empty-state">
      <span class="empty-icon">🔎</span>
      <p>No saved pin matches.<br>Press <strong>Enter</strong> to search the world map for this place.</p>
    </div>`;
    // Keep map pins visible even when local sidebar results are empty.
    setVisibleMarkers(state.pins);
  } else {
    renderSidebarPinList(filtered);
    setVisibleMarkers(filtered);
  }
}

async function searchWorldPlace(query) {
  const q = query.trim();
  if (!q) return;
  try {
    const url = `https://nominatim.openstreetmap.org/search?format=json&limit=1&q=${encodeURIComponent(q)}`;
    const res = await fetch(url, {
      headers: {
        Accept: 'application/json',
      },
    });
    if (!res.ok) throw new Error('Search service unavailable');
    const results = await res.json();
    if (!Array.isArray(results) || !results.length) {
      showToast('No world place found for that search', 'error');
      return;
    }
    const first = results[0];
    const lat = parseFloat(first.lat);
    const lng = parseFloat(first.lon);
    if (Number.isNaN(lat) || Number.isNaN(lng)) {
      showToast('Found result but coordinates were invalid', 'error');
      return;
    }
    const searchInput = document.getElementById('searchInput');
    searchInput.value = '';
    filterPins('');
    map.flyTo([lat, lng], Math.max(map.getZoom(), 8), { animate: true, duration: 1.2 });
    setTimeout(() => showAddModal(lat, lng), 700);
    showToast(`Found ${first.display_name.split(',')[0]}. Add a pin if you like.`, 'success');
  } catch (err) {
    showToast(`Place search failed: ${err.message}`, 'error');
  }
}

function initSearchInput() {
  const searchInput = document.getElementById('searchInput');
  searchInput.addEventListener('keydown', async (e) => {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    await searchWorldPlace(searchInput.value);
  });
}

function updateStats() {
  updateDistanceStats();
  void updateCountryStatsAsync();
}

function updateDistanceStats() {
  const distanceEl = document.getElementById('distanceRange');
  const distanceStat = document.getElementById('distanceStat');
  if (!distanceEl) return;

  const routeKm = computeConnectedRouteDistanceKm(state.routeConnections, state.pins);
  if (routeKm !== null) {
    const routeMi = kmToMiles(routeKm);
    distanceEl.textContent = formatDistance(routeKm);
    if (distanceStat) {
      distanceStat.title = `Connected route distance: ${formatDistance(routeKm)} km (${formatDistance(routeMi)} mi)`;
    }
    return;
  }

  const estimates = computeDistanceEstimates(state.pins);
  const minKm = estimates.minKm;
  const maxKm = estimates.maxKm;
  const minMi = kmToMiles(minKm);
  const maxMi = kmToMiles(maxKm);

  distanceEl.textContent = `${formatDistance(minKm)}-${formatDistance(maxKm)}`;
  if (distanceStat) {
    distanceStat.title = `Estimated range: ${formatDistance(minKm)}-${formatDistance(maxKm)} km (${formatDistance(minMi)}-${formatDistance(maxMi)} mi)`;
  }
}

function kmToMiles(km) {
  return km * 0.621371;
}

function formatDistance(value) {
  if (!Number.isFinite(value)) return '0';
  if (value >= 1000) return Math.round(value).toLocaleString();
  if (value >= 100) return value.toFixed(1);
  return value.toFixed(2);
}

function computeDistanceEstimates(pins) {
  const points = pins
    .map((pin) => ({
      lat: Number(pin.latitude),
      lng: Number(pin.longitude),
    }))
    .filter((p) => Number.isFinite(p.lat) && Number.isFinite(p.lng));

  if (points.length < 2) {
    return { minKm: 0, maxKm: 0 };
  }

  const mstKm = computeMSTDistanceKm(points);
  const farthestGreedyKm = computeGreedyPathDistanceKm(points, true);
  const maxKm = Math.max(mstKm, farthestGreedyKm);

  return {
    minKm: mstKm,
    maxKm,
  };
}

function computeConnectedRouteDistanceKm(routeConnections, pins) {
  if (!Array.isArray(routeConnections) || routeConnections.length === 0) {
    return null;
  }

  const pinMap = new Map();
  pins.forEach((pin) => {
    const lat = Number(pin.latitude);
    const lng = Number(pin.longitude);
    if (!Number.isFinite(lat) || !Number.isFinite(lng)) return;
    pinMap.set(pin.id, { lat, lng });
  });

  let total = 0;
  let validLegs = 0;
  routeConnections.forEach((route) => {
    const from = pinMap.get(route.from_pin_id);
    const to = pinMap.get(route.to_pin_id);
    if (!from || !to) return;
    total += haversineDistanceKm(from, to);
    validLegs += 1;
  });

  if (validLegs === 0) return null;
  return total;
}

function haversineDistanceKm(a, b) {
  const toRad = (deg) => (deg * Math.PI) / 180;
  const earthRadiusKm = 6371;
  const dLat = toRad(b.lat - a.lat);
  const dLng = toRad(b.lng - a.lng);
  const lat1 = toRad(a.lat);
  const lat2 = toRad(b.lat);

  const sinLat = Math.sin(dLat / 2);
  const sinLng = Math.sin(dLng / 2);
  const h = sinLat * sinLat + Math.cos(lat1) * Math.cos(lat2) * sinLng * sinLng;
  const c = 2 * Math.atan2(Math.sqrt(h), Math.sqrt(1 - h));
  return earthRadiusKm * c;
}

function computeMSTDistanceKm(points) {
  const n = points.length;
  const visited = new Array(n).fill(false);
  const minEdge = new Array(n).fill(Number.POSITIVE_INFINITY);
  minEdge[0] = 0;
  let total = 0;

  for (let i = 0; i < n; i += 1) {
    let next = -1;
    let best = Number.POSITIVE_INFINITY;
    for (let j = 0; j < n; j += 1) {
      if (!visited[j] && minEdge[j] < best) {
        best = minEdge[j];
        next = j;
      }
    }
    if (next === -1) break;
    visited[next] = true;
    total += best;

    for (let j = 0; j < n; j += 1) {
      if (visited[j]) continue;
      const d = haversineDistanceKm(points[next], points[j]);
      if (d < minEdge[j]) minEdge[j] = d;
    }
  }

  return total;
}

function computeGreedyPathDistanceKm(points, useFarthest) {
  const n = points.length;
  const visited = new Array(n).fill(false);
  let current = 0;
  let total = 0;
  visited[current] = true;

  for (let step = 1; step < n; step += 1) {
    let pick = -1;
    let pickDistance = useFarthest ? -1 : Number.POSITIVE_INFINITY;

    for (let i = 0; i < n; i += 1) {
      if (visited[i]) continue;
      const d = haversineDistanceKm(points[current], points[i]);
      if ((useFarthest && d > pickDistance) || (!useFarthest && d < pickDistance)) {
        pick = i;
        pickDistance = d;
      }
    }

    if (pick === -1 || !Number.isFinite(pickDistance)) break;
    visited[pick] = true;
    total += pickDistance;
    current = pick;
  }

  return total;
}

async function updateCountryStatsAsync() {
  const runId = ++state.statsRunId;
  document.getElementById('pinCount').textContent = state.pins.length;

  const countryCodes = new Set();
  for (const pin of state.pins) {
    const cachedCode = getCachedCountryCode(pin);
    if (cachedCode) countryCodes.add(cachedCode);
  }
  document.getElementById('countryCount').textContent = countryCodes.size;

  const missingPins = state.pins.filter((pin) => !getCachedCountryCode(pin));
  for (const pin of missingPins) {
    const code = await resolveCountryCode(pin);
    if (runId !== state.statsRunId) return;
    if (code) {
      countryCodes.add(code);
      document.getElementById('countryCount').textContent = countryCodes.size;
    }
    // Keep requests gentle to avoid geocoder throttling.
    await delay(300);
  }
}

// ─── Sidebar Navigation ──────────────────────────────────────
function flyToPin(id) {
  const pin = state.pins.find(p => p.id === id);
  if (!pin) return;
  state.activeId = id;
  highlightSidebarCard(id);
  map.flyTo([pin.latitude, pin.longitude], Math.max(map.getZoom(), 8), { animate: true, duration: 1.2 });
  setTimeout(() => {
    const marker = state.markers[id];
    if (marker) marker.openPopup();
  }, 1300);
}

function highlightSidebarCard(id) {
  document.querySelectorAll('.pin-card').forEach(el => el.classList.remove('active'));
  const card = document.getElementById(`card-${id}`);
  if (card) { card.classList.add('active'); card.scrollIntoView({ behavior: 'smooth', block: 'nearest' }); }
}

// ─── Add/Edit Pin Modal ───────────────────────────────────────
function showAddModal(lat, lng) {
  if (!requireAdminAccess()) return;
  state.editingId = null;
  state.addClickMode = false;
  state.pendingPickDraft = null;
  document.getElementById('modalTitle').textContent = '📍 New Pin';
  document.getElementById('pinForm').reset();
  document.getElementById('pinId').value = '';
  document.getElementById('pinColor').value = '#FF5722';
  document.getElementById('pinIcon').value = '📍';
  state.pinImageList = [];
  renderPinImageListEditor();
  document.getElementById('pinImageUploadStatus').textContent = 'No file uploaded';
  document.getElementById('coordHint').textContent = '💡 Or click anywhere on the map to set coordinates';
  if (lat !== undefined) {
    document.getElementById('pinLat').value = lat.toFixed(6);
    document.getElementById('pinLng').value = lng.toFixed(6);
  }
  openModal('pinModal');
}

function editPinById(id) {
  if (!requireAdminAccess()) return;
  const pin = state.pins.find(p => p.id === id);
  if (!pin) return;
  state.editingId = id;
  state.pendingPickDraft = null;
  document.getElementById('modalTitle').textContent = '✏️ Edit Pin';
  document.getElementById('pinId').value   = pin.id;
  document.getElementById('pinTitle').value       = pin.title;
  document.getElementById('pinDescription').value = pin.description || '';
  const images = getPinImages(pin);
  state.pinImageList = [...images];
  document.getElementById('pinImageUrl').value    = '';
  renderPinImageListEditor();
  document.getElementById('pinLat').value         = pin.latitude;
  document.getElementById('pinLng').value         = pin.longitude;
  document.getElementById('pinColor').value       = pin.color || '#FF5722';
  document.getElementById('pinIcon').value        = pin.icon || '📍';
  document.getElementById('pinVisitedAt').value   = pin.visited_at || '';
  document.getElementById('pinImageUploadStatus').textContent = images.length ? `${images.length} image(s) set` : 'No file uploaded';
  openModal('pinModal');
}

function closePinModal(keepAddClickMode = false) {
  closeModal('pinModal');
  if (!keepAddClickMode) {
    state.addClickMode = false;
    state.pendingPickDraft = null;
    map.getContainer().style.cursor = '';
  }
}

async function savePin(e) {
  e.preventDefault();
  const pendingTypedURLs = parseImageURLList(document.getElementById('pinImageUrl').value);
  state.pinImageList = dedupeStrings(state.pinImageList.concat(pendingTypedURLs));
  document.getElementById('pinImageUrl').value = '';
  renderPinImageListEditor();
  const data = {
    title:       document.getElementById('pinTitle').value.trim(),
    description: document.getElementById('pinDescription').value.trim(),
    image_url:   state.pinImageList.join('\n'),
    latitude:    parseFloat(document.getElementById('pinLat').value),
    longitude:   parseFloat(document.getElementById('pinLng').value),
    color:       document.getElementById('pinColor').value,
    icon:        document.getElementById('pinIcon').value,
    visited_at:  document.getElementById('pinVisitedAt').value.trim(),
  };

  try {
    if (state.editingId) {
      const updated = await updatePin(state.editingId, data);
      const idx = state.pins.findIndex(p => p.id === state.editingId);
      if (idx !== -1) state.pins[idx] = updated;
      removeMarkerFromMap(state.editingId);
      addMarkerToMap(updated);
      showToast(`Updated "${updated.title}" ✅`, 'success');
    } else {
      const created = await createPin(data);
      state.pins.unshift(created);
      addMarkerToMap(created);
      map.flyTo([created.latitude, created.longitude], 8, { animate: true, duration: 1.2 });
      showToast(`Added "${created.title}" 📍`, 'success');
    }
    renderSidebarPinList(state.pins);
    setVisibleMarkers(state.pins);
    renderRouteConnections();
    updateStats();
    closePinModal();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function deletePinById(id) {
  if (!requireAdminAccess()) return;
  const pin = state.pins.find(p => p.id === id);
  if (!pin) return;
  if (!confirm(`Remove "${pin.title}" from your map?`)) return;
  try {
    await deletePin(id);
    state.pins = state.pins.filter(p => p.id !== id);
    removeMarkerFromMap(id);
    renderSidebarPinList(state.pins);
    await loadRoutes();
    updateStats();
    map.closePopup();
    showToast(`Removed "${pin.title}"`, 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// ─── Icon & Color pickers ─────────────────────────────────────
function setIcon(icon) {
  document.getElementById('pinIcon').value = icon;
}

function setColor(hex) {
  document.getElementById('pinColor').value = hex;
}

// ─── Import Modal ─────────────────────────────────────────────
function showImportModal() {
  if (!requireAdminAccess()) return;
  openModal('importModal');
}
function closeImportModal() { closeModal('importModal'); }

function showCloneModal() {
  if (!requireAdminAccess()) return;
  openModal('cloneModal');
  void refreshCloneMeta();
}

function closeCloneModal() {
  closeModal('cloneModal');
}

function showTitleSettingsModal() {
  if (!requireAdminAccess()) return;
  document.getElementById('mapDisplayTitle').value = state.profileTitle || '';
  openModal('titleSettingsModal');
}

function closeTitleSettingsModal() {
  closeModal('titleSettingsModal');
}

async function saveTitleSettings(event) {
  event.preventDefault();
  if (!requireAdminAccess()) return;
  const input = document.getElementById('mapDisplayTitle');
  const nextTitle = input.value.trim();
  try {
    await updateProfileDisplayTitle(nextTitle);
    state.profileTitle = nextTitle;
    applyMapTitle(state.profileTitle);
    closeTitleSettingsModal();
    showToast('Map title updated', 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function createCloneInvite() {
  if (!requireAdminAccess()) return;
  const includePhotos = document.getElementById('cloneIncludePhotos').checked;
  const expiresHours = parseInt(document.getElementById('cloneExpiresHours').value, 10) || 72;
  try {
    const result = await createCloneInviteRequest({ include_photos: includePhotos, expires_hours: expiresHours, max_uses: 1 });
    const tokenField = document.getElementById('cloneInviteToken');
    tokenField.value = result.invite_token || '';
    tokenField.focus();
    tokenField.select();
    showToast('Clone invite created. Share the token safely.', 'success');
    await refreshCloneMeta();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function parseCloneTokenInput(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  try {
    const asUrl = new URL(value);
    return asUrl.searchParams.get('cloneInvite') || value;
  } catch {
    return value;
  }
}

async function copyCloneInviteLink() {
  const token = document.getElementById('cloneInviteToken').value.trim();
  if (!token) {
    showToast('Create an invite token first', 'error');
    return;
  }
  const link = `${window.location.origin}${window.location.pathname}?cloneInvite=${encodeURIComponent(token)}`;
  try {
    await navigator.clipboard.writeText(link);
    showToast('Invite link copied', 'success');
  } catch {
    showToast('Could not copy to clipboard', 'error');
  }
}

async function acceptCloneInvite() {
  if (!requireAdminAccess()) return;
  const token = parseCloneTokenInput(document.getElementById('cloneAcceptToken').value);
  if (!token) {
    showToast('Paste an invite token first', 'error');
    return;
  }
  try {
    const result = await acceptCloneInviteRequest(token);
    await loadPins();
    await loadRoutes();
    document.getElementById('cloneAcceptToken').value = '';
    await refreshCloneMeta();
    showToast(result.message || 'Map cloned successfully', 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function formatDateTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString()}`;
}

function renderCloneInvites(invites) {
  const list = document.getElementById('cloneInviteList');
  if (!list) return;
  if (!invites.length) {
    list.innerHTML = '<div class="clone-empty">No invites created yet.</div>';
    return;
  }
  list.innerHTML = invites.map((invite) => {
    const status = invite.revoked
      ? 'Revoked'
      : (invite.used_count >= invite.max_uses ? 'Used' : (new Date(invite.expires_at) < new Date() ? 'Expired' : 'Active'));
    const canRevoke = status === 'Active';
    return `<div class="clone-item">
      <div class="clone-item-main">
        <div class="clone-item-title">Invite #${invite.id} <span class="clone-status ${status.toLowerCase()}">${status}</span></div>
        <div class="clone-item-meta">Photos: ${invite.include_photos ? 'Yes' : 'No'} · Uses: ${invite.used_count}/${invite.max_uses} · Expires: ${formatDateTime(invite.expires_at)}</div>
      </div>
      ${canRevoke ? `<button class="btn btn-ghost" type="button" onclick="revokeCloneInvite(${invite.id})">Revoke</button>` : ''}
    </div>`;
  }).join('');
}

function renderCloneEvents(events) {
  const list = document.getElementById('cloneActivityList');
  if (!list) return;
  if (!events.length) {
    list.innerHTML = '<div class="clone-empty">No clone activity yet.</div>';
    return;
  }
  list.innerHTML = events.map((event) => `
    <div class="clone-item">
      <div class="clone-item-main">
        <div class="clone-item-title">${escapeHTML(event.target_owner_key || 'unknown user')}</div>
        <div class="clone-item-meta">${event.cloned_count} pin(s) · Photos: ${event.include_photos ? 'Yes' : 'No'} · ${formatDateTime(event.created_at)}</div>
      </div>
    </div>
  `).join('');
}

async function refreshCloneMeta() {
  if (!state.isAdmin) return;
  try {
    const [invites, events] = await Promise.all([fetchCloneInvites(), fetchCloneEvents()]);
    renderCloneInvites(invites);
    renderCloneEvents(events);
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function revokeCloneInvite(inviteId) {
  try {
    await revokeCloneInviteRequest(inviteId);
    showToast('Invite revoked', 'success');
    await refreshCloneMeta();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function handleFileDrop(e) {
  e.preventDefault();
  document.getElementById('dropZone').classList.remove('drag-over');
  const file = e.dataTransfer.files[0];
  if (file) readFileIntoTextarea(file);
}

function handleFileSelect(e) {
  const file = e.target.files[0];
  if (file) readFileIntoTextarea(file);
}

function readFileIntoTextarea(file) {
  const reader = new FileReader();
  reader.onload = (e) => { document.getElementById('importJson').value = e.target.result; };
  reader.readAsText(file);
}

async function submitImport() {
  const json = document.getElementById('importJson').value.trim();
  const result = document.getElementById('importResult');
  result.className = 'import-result';
  result.style.display = 'none';

  if (!json) { showToast('Paste or drop a JSON file first', 'error'); return; }

  try {
    JSON.parse(json); // validate
  } catch {
    result.className = 'import-result error';
    result.textContent = '❌ Invalid JSON — please check your file.';
    result.style.display = 'block';
    return;
  }

  try {
    const res = await fetch('/api/import/googlemaps', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: json,
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Import failed');
    result.className = 'import-result success';
    result.textContent = `✅ ${data.message}`;
    result.style.display = 'block';
    await loadPins();
    showToast(data.message, 'success');
    setTimeout(closeImportModal, 1800);
  } catch (err) {
    result.className = 'import-result error';
    result.textContent = `❌ ${err.message}`;
    result.style.display = 'block';
  }
}

async function submitPhotoImport() {
  showToast('Photo JSON import was replaced. Use Upload From Device in the pin form.', 'error');
}

async function uploadPinImageFromDevice(event) {
  if (!requireAdminAccess()) return;
  const files = Array.from(event.target.files || []);
  if (!files.length) return;

  const statusEl = document.getElementById('pinImageUploadStatus');
  statusEl.textContent = `Uploading ${files.length} file(s)...`;

  const uploadedURLs = [];

  try {
    for (const file of files) {
      const formData = new FormData();
      formData.append('image', file);
      const res = await fetch('/api/upload/image', {
        method: 'POST',
        body: formData,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Upload failed');
      const normalized = normalizeImageURL(data.url || '');
      if (normalized) uploadedURLs.push(normalized);
    }

    const merged = dedupeStrings(state.pinImageList.concat(uploadedURLs));
    state.pinImageList = merged;
    renderPinImageListEditor();
    document.getElementById('pinImageUrl').value = '';
    statusEl.textContent = `${uploadedURLs.length} image(s) uploaded`;
    showToast('Photos uploaded. Save pin to attach them.', 'success');
  } catch (err) {
    statusEl.textContent = 'Upload failed';
    showToast(`Upload failed: ${err.message}`, 'error');
  } finally {
    event.target.value = '';
  }
}

function addImageURLToPinList() {
  if (!requireAdminAccess()) return;
  const input = document.getElementById('pinImageUrl');
  const toAdd = parseImageURLList(input.value);
  if (!toAdd.length) {
    showToast('Enter a valid image URL first', 'error');
    return;
  }
  state.pinImageList = dedupeStrings(state.pinImageList.concat(toAdd));
  input.value = '';
  renderPinImageListEditor();
  showToast('Image URL added', 'success');
}

function renderPinImageListEditor() {
  const container = document.getElementById('pinImageList');
  if (!container) return;
  if (!state.pinImageList.length) {
    container.innerHTML = '<div class="pin-image-empty">No photos yet. Upload or add a URL, then drag to reorder.</div>';
    return;
  }
  container.innerHTML = state.pinImageList.map((url, idx) => `
    <div class="pin-image-item ${idx === 0 ? 'cover' : ''}" draggable="true"
      ondragstart="startPinImageDrag(event, ${idx})"
      ondragover="allowPinImageDrop(event)"
      ondrop="dropPinImageAt(event, ${idx})">
      <img src="${url}" alt="Pin photo ${idx + 1}" loading="lazy" referrerpolicy="no-referrer" />
      <span class="pin-image-badge">${idx === 0 ? 'Cover' : `#${idx + 1}`}</span>
      <div class="pin-image-actions">
        <button type="button" onclick="setCoverImage(${idx})">Cover</button>
        <button type="button" onclick="removePinImage(${idx})">Remove</button>
      </div>
    </div>
  `).join('');
}

function startPinImageDrag(event, index) {
  state.draggedImageIndex = index;
  event.dataTransfer.effectAllowed = 'move';
}

function allowPinImageDrop(event) {
  event.preventDefault();
  event.dataTransfer.dropEffect = 'move';
}

function dropPinImageAt(event, dropIndex) {
  event.preventDefault();
  const from = state.draggedImageIndex;
  state.draggedImageIndex = null;
  if (from === null || from === dropIndex) return;
  const moved = state.pinImageList.splice(from, 1)[0];
  state.pinImageList.splice(dropIndex, 0, moved);
  renderPinImageListEditor();
}

function setCoverImage(index) {
  if (index <= 0 || index >= state.pinImageList.length) return;
  const selected = state.pinImageList.splice(index, 1)[0];
  state.pinImageList.unshift(selected);
  renderPinImageListEditor();
  showToast('Cover photo updated', 'success');
}

function removePinImage(index) {
  state.pinImageList.splice(index, 1);
  renderPinImageListEditor();
}

function openGalleryForPin(id) {
  const pin = state.pins.find((p) => p.id === id);
  if (!pin) return;
  const images = getPinImages(pin);
  if (!images.length) {
    showToast('No photos available for this pin', 'error');
    return;
  }

  state.lightboxImages = images;
  state.lightboxImageTitles = images.map(() => pin.title);
  state.lightboxTitleBase = pin.title;
  state.lightboxIndex = 0;

  document.getElementById('galleryTitle').textContent = `${pin.title} - Photo Gallery`;
  document.getElementById('galleryGrid').innerHTML = images.map((url, idx) =>
    `<button type="button" onclick="openLightboxModal(${idx})" title="Open photo ${idx + 1}">
      <img src="${url}" alt="${escapeHTML(pin.title)} photo ${idx + 1}" loading="lazy" referrerpolicy="no-referrer" />
    </button>`
  ).join('');
  openModal('galleryModal');
}

function closeGalleryModal() {
  closeModal('galleryModal');
}

function isModalOpen(id) {
  const modal = document.getElementById(id);
  return !!modal && modal.classList.contains('open');
}

function setLightboxImage(index) {
  if (!state.lightboxImages.length) return;
  const total = state.lightboxImages.length;
  const normalizedIndex = ((index % total) + total) % total;
  state.lightboxIndex = normalizedIndex;

  const image = document.getElementById('lightboxImage');
  const title = document.getElementById('lightboxTitle');
  const counter = document.getElementById('lightboxCounter');

  const imageTitle = state.lightboxImageTitles[normalizedIndex] || state.lightboxTitleBase;

  image.src = state.lightboxImages[normalizedIndex];
  image.alt = `${imageTitle} photo ${normalizedIndex + 1}`;
  title.textContent = `${imageTitle} - Photo ${normalizedIndex + 1}`;
  counter.textContent = `${normalizedIndex + 1} / ${total}`;
}

function openLightboxModal(indexOrUrl = 0, title = 'Photo', index = 1) {
  if (typeof indexOrUrl === 'string') {
    const normalized = normalizeImageURL(indexOrUrl);
    if (!normalized) return;
    state.lightboxImages = [normalized];
    state.lightboxImageTitles = [title || 'Photo'];
    state.lightboxTitleBase = title || 'Photo';
    state.lightboxIndex = Math.max(0, Number(index) - 1);
  } else {
    state.lightboxIndex = Number(indexOrUrl) || 0;
    if (!state.lightboxTitleBase) {
      state.lightboxTitleBase = title || 'Photo';
    }
  }

  if (!state.lightboxImages.length) {
    showToast('No photos available for this pin', 'error');
    return;
  }

  openModal('lightboxModal');
  setLightboxImage(state.lightboxIndex);
}

function showNextLightboxImage() {
  if (!isModalOpen('lightboxModal')) return;
  setLightboxImage(state.lightboxIndex + 1);
}

function showPreviousLightboxImage() {
  if (!isModalOpen('lightboxModal')) return;
  setLightboxImage(state.lightboxIndex - 1);
}

function closeLightboxModal() {
  stopSlideshowPlayback();
  document.getElementById('lightboxImage').src = '';
  document.getElementById('lightboxCounter').textContent = '1 / 1';
  state.lightboxImages = [];
  state.lightboxImageTitles = [];
  state.lightboxIndex = 0;
  state.lightboxTitleBase = 'Photo';
  closeModal('lightboxModal');
}

async function fetchAuthStatus() {
  const res = await fetch('/api/auth/status', { method: 'GET' });
  if (!res.ok) throw new Error('Failed to load auth status');
  return res.json();
}

async function loginAdmin(password) {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Login failed');
  return data;
}

async function loginWithGoogle(credential) {
  const res = await fetch('/api/auth/google', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ credential }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Google login failed');
  return data;
}

async function logoutAdmin() {
  await fetch('/api/auth/logout', { method: 'POST' });
}

function updateAuthUI() {
  const authBtn = document.getElementById('authBtn');
  if (authBtn) {
    if (!state.authEnabled) {
      authBtn.style.display = 'none';
    } else if (state.isAdmin) {
      authBtn.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M12 1a5 5 0 00-5 5v3H6a2 2 0 00-2 2v9a2 2 0 002 2h12a2 2 0 002-2v-9a2 2 0 00-2-2h-1V6a5 5 0 00-5-5zm-3 8V6a3 3 0 016 0v3H9z"/></svg> Logout';
      authBtn.title = 'Logout admin';
    } else {
      authBtn.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M12 1a5 5 0 00-5 5v3H6a2 2 0 00-2 2v9a2 2 0 002 2h12a2 2 0 002-2v-9a2 2 0 00-2-2h-1V6a5 5 0 00-5-5zm-3 8V6a3 3 0 016 0v3H9z"/></svg> Sign In';
      authBtn.title = 'Sign in';
    }
  }

  const modalTitle = document.getElementById('authModalTitle');
  if (modalTitle) {
    modalTitle.textContent = state.googleLoginEnabled ? '🔐 Sign In to Your Map' : '🔐 Admin Login';
  }
  const passwordLabel = document.getElementById('authPasswordLabel');
  if (passwordLabel) {
    passwordLabel.textContent = state.passwordLoginEnabled ? 'Password' : 'Password login disabled';
  }
  const passwordInput = document.getElementById('adminPassword');
  if (passwordInput) {
    passwordInput.disabled = !state.passwordLoginEnabled;
  }
  const authForm = document.getElementById('authForm');
  if (authForm) {
    authForm.style.display = state.passwordLoginEnabled ? 'block' : 'none';
  }
  const googleWrap = document.getElementById('googleLoginWrap');
  if (googleWrap) {
    googleWrap.style.display = state.googleLoginEnabled ? 'block' : 'none';
  }
  document.querySelectorAll('.admin-only-action').forEach((el) => {
    el.style.display = (!state.authEnabled || state.isAdmin) ? '' : 'none';
  });

  if (!state.isAdmin) {
    state.routeConnectMode = false;
    state.routeDragStartId = null;
    clearRouteGhostLine();
  }
  updateRouteModeUI();
  renderRouteConnections();

  renderSidebarPinList(state.pins);
  refreshMarkerPopups();
}

async function initAuthState() {
  try {
    const status = await fetchAuthStatus();
    state.authEnabled = !!status.auth_enabled;
    state.isAdmin = !!status.is_admin;
    state.passwordLoginEnabled = !!status.password_login_enabled;
    state.googleLoginEnabled = !!status.google_login_enabled;
    state.googleClientId = status.google_client_id || '';
  } catch {
    state.authEnabled = false;
    state.isAdmin = false;
    state.passwordLoginEnabled = false;
    state.googleLoginEnabled = false;
    state.googleClientId = '';
  }
  updateAuthUI();
  if (state.googleLoginEnabled && state.googleClientId) {
    void ensureGoogleAuthReady();
  }
}

function handleAuthButtonClick() {
  if (!state.authEnabled) return;
  if (state.isAdmin) {
    void (async () => {
      await logoutAdmin();
      state.isAdmin = false;
      state.pins = [];
      state.routeConnections = [];
      clusterGroup.clearLayers();
      state.markers = {};
      state.profileTitle = '';
      applyMapTitle('');
      renderRouteConnections();
      updateAuthUI();
      renderSidebarPinList(state.pins);
      updateStats();
      showToast('Logged out', 'success');
    })();
    return;
  }
  openModal('authModal');
}

function closeAuthModal() {
  closeModal('authModal');
  const form = document.getElementById('authForm');
  if (form) form.reset();
}

async function submitAdminLogin(event) {
  event.preventDefault();
  if (!state.passwordLoginEnabled) return;
  const password = document.getElementById('adminPassword').value;
  try {
    await loginAdmin(password);
    state.isAdmin = true;
    updateAuthUI();
    closeAuthModal();
    await loadProfileTitle();
    await loadPins();
    await loadRoutes();
    showToast('Admin access enabled', 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function loadGoogleIdentityScript() {
  if (state.googleScriptLoaded) return Promise.resolve();
  if (window.google?.accounts?.id) {
    state.googleScriptLoaded = true;
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    const existing = document.querySelector('script[data-google-identity="true"]');
    if (existing) {
      existing.addEventListener('load', () => {
        state.googleScriptLoaded = true;
        resolve();
      }, { once: true });
      existing.addEventListener('error', reject, { once: true });
      return;
    }
    const script = document.createElement('script');
    script.src = 'https://accounts.google.com/gsi/client';
    script.async = true;
    script.defer = true;
    script.dataset.googleIdentity = 'true';
    script.addEventListener('load', () => {
      state.googleScriptLoaded = true;
      resolve();
    }, { once: true });
    script.addEventListener('error', () => reject(new Error('Failed to load Google sign-in script')), { once: true });
    document.head.appendChild(script);
  });
}

async function ensureGoogleAuthReady() {
  await loadGoogleIdentityScript();
  if (!window.google?.accounts?.id) return;
  window.google.accounts.id.initialize({
    client_id: state.googleClientId,
    callback: async (response) => {
      try {
        await loginWithGoogle(response.credential);
        state.isAdmin = true;
        updateAuthUI();
        closeAuthModal();
        await loadProfileTitle();
        await loadPins();
        await loadRoutes();
        showToast('Signed in with Google', 'success');
      } catch (err) {
        showToast(err.message, 'error');
      }
    },
    auto_select: false,
  });
  const target = document.getElementById('googleLoginButton');
  if (target) {
    target.innerHTML = '';
    window.google.accounts.id.renderButton(target, {
      theme: 'outline',
      size: 'large',
      shape: 'pill',
      text: 'continue_with',
      width: 280,
    });
  }
}

function buildGlobalSlideshowPool() {
  const pool = [];
  state.pins.forEach((pin) => {
    const images = getPinImages(pin);
    images.forEach((url) => {
      pool.push({ url, title: pin.title || 'Photo' });
    });
  });
  return pool;
}

function shuffleArray(items) {
  const copy = [...items];
  for (let i = copy.length - 1; i > 0; i -= 1) {
    const j = Math.floor(Math.random() * (i + 1));
    const tmp = copy[i];
    copy[i] = copy[j];
    copy[j] = tmp;
  }
  return copy;
}

function startSlideshowTimer() {
  if (state.slideshowTimerId) {
    clearInterval(state.slideshowTimerId);
    state.slideshowTimerId = null;
  }
  state.slideshowTimerId = setInterval(() => {
    if (!state.slideshowActive || !isModalOpen('lightboxModal')) return;
    showNextLightboxImage();
  }, state.slideshowDelayMs);
}

function stopSlideshowTimer() {
  if (!state.slideshowTimerId) return;
  clearInterval(state.slideshowTimerId);
  state.slideshowTimerId = null;
}

function updateSlideshowControls() {
  const headerBtn = document.getElementById('slideshowBtn');
  if (headerBtn) {
    headerBtn.innerHTML = state.slideshowActive
      ? '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M6 6h12v12H6z"/></svg> Stop Show'
      : '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M8 5v14l11-7z"/></svg> Slideshow';
  }
  const stopBtn = document.getElementById('slideshowStopBtn');
  if (stopBtn) {
    stopBtn.style.display = state.slideshowActive ? 'inline-flex' : 'none';
  }
}

async function requestSlideshowFullscreen() {
  const target = document.getElementById('lightboxModal') || document.documentElement;
  if (!target || document.fullscreenElement) return;

  const request = target.requestFullscreen
    || target.webkitRequestFullscreen
    || target.mozRequestFullScreen
    || target.msRequestFullscreen;
  if (!request) return;

  try {
    await request.call(target);
  } catch {
    // Fullscreen can fail if browser blocks user gesture chaining.
  }
}

async function exitSlideshowFullscreen() {
  const exit = document.exitFullscreen
    || document.webkitExitFullscreen
    || document.mozCancelFullScreen
    || document.msExitFullscreen;
  if (!exit) return;

  try {
    await exit.call(document);
  } catch {
    // Ignore exit failures.
  }
}

async function startRandomSlideshow() {
  const pool = buildGlobalSlideshowPool();
  if (!pool.length) {
    showToast('No photos found. Add or upload images first.', 'error');
    return;
  }

  const randomized = shuffleArray(pool);
  state.lightboxImages = randomized.map((item) => item.url);
  state.lightboxImageTitles = randomized.map((item) => item.title);
  state.lightboxTitleBase = 'Slideshow';
  state.lightboxIndex = 0;
  state.slideshowActive = true;
  document.body.classList.add('slideshow-mode');
  updateSlideshowControls();

  openModal('lightboxModal');
  setLightboxImage(0);
  startSlideshowTimer();
  await requestSlideshowFullscreen();
}

function toggleSlideshow() {
  if (state.slideshowActive) {
    closeLightboxModal();
    return;
  }
  void requestSlideshowFullscreen();
  void startRandomSlideshow();
}

function stopSlideshowPlayback() {
  if (!state.slideshowActive && !state.slideshowTimerId) return;
  state.slideshowActive = false;
  document.body.classList.remove('slideshow-mode');
  stopSlideshowTimer();
  updateSlideshowControls();
  void exitSlideshowFullscreen();
}

// ─── Sidebar toggle ───────────────────────────────────────────
function toggleSidebar() {
  const sidebar = document.getElementById('sidebar');
  const openBtn  = document.getElementById('sidebarOpenBtn');
  const collapsed = sidebar.classList.toggle('collapsed');
  openBtn.style.display = collapsed ? 'flex' : 'none';
}

// ─── Modal helpers ────────────────────────────────────────────
function openModal(id) {
  const el = document.getElementById(id);
  el.style.display = 'flex';
  requestAnimationFrame(() => el.classList.add('open'));
  document.addEventListener('keydown', handleEscKey);
}

function closeModal(id) {
  const el = document.getElementById(id);
  el.classList.remove('open');
  // Hide immediately; waiting for transitionend can feel laggy on slower devices.
  el.style.display = 'none';
  document.removeEventListener('keydown', handleEscKey);
}

function handleEscKey(e) {
  if (e.key === 'Escape') {
    closeTitleSettingsModal();
    closeCloneModal();
    closeAuthModal();
    closePinModal();
    closeImportModal();
    closeGalleryModal();
    closeLightboxModal();
    return;
  }

  if (e.key === 'ArrowRight' && isModalOpen('lightboxModal')) {
    e.preventDefault();
    showNextLightboxImage();
  }

  if (e.key === 'ArrowLeft' && isModalOpen('lightboxModal')) {
    e.preventDefault();
    showPreviousLightboxImage();
  }
}

// Close modal on overlay click
document.querySelectorAll('.modal-overlay').forEach(overlay => {
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay) {
      closeTitleSettingsModal();
      closeCloneModal();
      closeAuthModal();
      closePinModal();
      closeImportModal();
      closeGalleryModal();
      closeLightboxModal();
    }
  });
});

const lightboxModalEl = document.getElementById('lightboxModal');
if (lightboxModalEl) {
  lightboxModalEl.addEventListener('wheel', (e) => {
    if (!isModalOpen('lightboxModal') || state.lightboxImages.length < 2) return;
    e.preventDefault();
    if (e.deltaY > 0) {
      showNextLightboxImage();
    } else if (e.deltaY < 0) {
      showPreviousLightboxImage();
    }
  }, { passive: false });
}

// ─── Toast ────────────────────────────────────────────────────
let toastTimer;
function showToast(msg, type = '') {
  const toast = document.getElementById('toast');
  clearTimeout(toastTimer);
  toast.textContent = msg;
  toast.className = `toast ${type}`;
  requestAnimationFrame(() => toast.classList.add('show'));
  toastTimer = setTimeout(() => toast.classList.remove('show'), 3000);
}

// ─── Utility ─────────────────────────────────────────────────
function escapeHTML(str) {
  const d = document.createElement('div');
  d.appendChild(document.createTextNode(String(str)));
  return d.innerHTML;
}

function normalizeImageURL(url) {
  if (!url) return '';
  try {
    const parsed = new URL(url, window.location.origin);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return '';
    }
    return parsed.toString();
  } catch {
    return '';
  }
}

function parseImageURLList(raw) {
  if (!raw) return [];
  const text = String(raw).trim();

  // Handle concatenated URLs with no separators, e.g. "...jpghttp://..."
  // by splitting at each protocol boundary.
  const protocolChunks = text.match(/https?:\/\/[\s\S]*?(?=https?:\/\/|$)/gi) || [];

  const candidates = protocolChunks.length
    ? protocolChunks
    : text.split(/[\n,\s]+/);

  const normalized = candidates
    .map((item) => normalizeImageURL(String(item).trim()))
    .filter(Boolean);

  return dedupeStrings(normalized);
}

function dedupeStrings(values) {
  const out = [];
  const seen = new Set();
  for (const value of values) {
    if (!seen.has(value)) {
      seen.add(value);
      out.push(value);
    }
  }
  return out;
}

function getPinImages(pin) {
  if (Array.isArray(pin.image_urls) && pin.image_urls.length) {
    return dedupeStrings(pin.image_urls.map((u) => normalizeImageURL(u)).filter(Boolean));
  }
  return parseImageURLList(pin.image_url || '');
}

// ─── Kick-off ─────────────────────────────────────────────────
async function bootstrapApp() {
  initSearchInput();
  updateSlideshowControls();
  updateRouteModeUI();
  await initAuthState();
  await loadProfileTitle();
  await loadPins();
  await loadRoutes();
  const params = new URLSearchParams(window.location.search);
  const tokenFromLink = params.get('cloneInvite');
  if (tokenFromLink) {
    document.getElementById('cloneAcceptToken').value = tokenFromLink;
    if (state.isAdmin) {
      showCloneModal();
    }
  }
}

void bootstrapApp();
