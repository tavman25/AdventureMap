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
};

// ─── Map initialisation ──────────────────────────────────────
const map = L.map('map', {
  center: [20, 0],
  zoom: 2,
  minZoom: 2,
  zoomControl: true,
  attributionControl: true,
});

// Dark base tile layer (CartoDB Dark)
L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
  attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors &copy; <a href="https://carto.com/">CARTO</a>',
  subdomains: 'abcd',
  maxZoom: 19,
}).addTo(map);

// MarkerCluster group
const clusterGroup = L.markerClusterGroup({
  showCoverageOnHover: false,
  maxClusterRadius: 50,
  spiderfyOnMaxZoom: true,
  disableClusteringAtZoom: 10,
});
map.addLayer(clusterGroup);

// Map click handler (for placing pins)
map.on('click', (e) => {
  if (state.addClickMode) {
    document.getElementById('pinLat').value = e.latlng.lat.toFixed(6);
    document.getElementById('pinLng').value = e.latlng.lng.toFixed(6);
    state.addClickMode = false;
    document.getElementById('coordHint').textContent = `✅ Coordinates set: ${e.latlng.lat.toFixed(4)}, ${e.latlng.lng.toFixed(4)}`;
    showToast('Coordinates picked! Fill in the form and save.', 'success');
  }
});

document.getElementById('coordHint').addEventListener('click', () => {
  state.addClickMode = true;
  document.getElementById('coordHint').textContent = '🖱️ Click on the map to set coordinates…';
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
      ${pin.description ? `<p class="popup-desc">${escapeHTML(pin.description)}</p>` : ''}
      <div class="popup-meta">${meta}</div>
      <div class="popup-actions">
        <button class="btn btn-ghost" onclick="editPinById(${pin.id})">✏️ Edit</button>
        <button class="btn btn-danger" onclick="deletePinById(${pin.id})">🗑 Delete</button>
      </div>
    </div>`;
}

function addMarkerToMap(pin) {
  const marker = L.marker([pin.latitude, pin.longitude], { icon: createPinIcon(pin) });
  marker.bindPopup(buildPopupHTML(pin), { maxWidth: 280 });
  marker.on('click', () => {
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

// ─── API Calls ───────────────────────────────────────────────
async function fetchPins() {
  const res = await fetch('/api/pins');
  if (!res.ok) throw new Error('Failed to load pins');
  return res.json();
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

// ─── Load & Render ───────────────────────────────────────────
async function loadPins() {
  try {
    state.pins = await fetchPins();
    renderSidebarPinList(state.pins);
    clusterGroup.clearLayers();
    state.markers = {};
    state.pins.forEach(addMarkerToMap);
    updateStats();
  } catch (err) {
    showToast('Could not load pins: ' + err.message, 'error');
  }
}

function renderSidebarPinList(pins) {
  const list = document.getElementById('pinList');
  if (!pins.length) {
    list.innerHTML = `<div class="empty-state">
      <span class="empty-icon">🌍</span>
      <p>No places yet!<br>Click <strong>Add Pin</strong> or click anywhere on the map to start your travel story.</p>
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
      <div class="pin-card-actions">
        <button title="Edit" onclick="event.stopPropagation(); editPinById(${p.id})">✏️</button>
        <button title="Delete" class="delete" onclick="event.stopPropagation(); deletePinById(${p.id})">🗑</button>
      </div>
    </div>`).join('');
}

function filterPins(query) {
  const q = query.toLowerCase();
  const filtered = state.pins.filter(p =>
    p.title.toLowerCase().includes(q) ||
    (p.description || '').toLowerCase().includes(q) ||
    (p.visited_at || '').toLowerCase().includes(q)
  );
  renderSidebarPinList(filtered);
}

function updateStats() {
  document.getElementById('pinCount').textContent = state.pins.length;
  // Rough country count: cluster unique ~1° lat/lng cells as proxy
  const buckets = new Set(state.pins.map(p => `${Math.round(p.latitude/5)},${Math.round(p.longitude/5)}`));
  document.getElementById('countryCount').textContent = buckets.size;
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
  state.editingId = null;
  state.addClickMode = false;
  document.getElementById('modalTitle').textContent = '📍 New Pin';
  document.getElementById('pinForm').reset();
  document.getElementById('pinId').value = '';
  document.getElementById('pinColor').value = '#FF5722';
  document.getElementById('pinIcon').value = '📍';
  document.getElementById('coordHint').textContent = '💡 Or click anywhere on the map to set coordinates';
  if (lat !== undefined) {
    document.getElementById('pinLat').value = lat.toFixed(6);
    document.getElementById('pinLng').value = lng.toFixed(6);
  }
  openModal('pinModal');
}

function editPinById(id) {
  const pin = state.pins.find(p => p.id === id);
  if (!pin) return;
  state.editingId = id;
  document.getElementById('modalTitle').textContent = '✏️ Edit Pin';
  document.getElementById('pinId').value   = pin.id;
  document.getElementById('pinTitle').value       = pin.title;
  document.getElementById('pinDescription').value = pin.description || '';
  document.getElementById('pinLat').value         = pin.latitude;
  document.getElementById('pinLng').value         = pin.longitude;
  document.getElementById('pinColor').value       = pin.color || '#FF5722';
  document.getElementById('pinIcon').value        = pin.icon || '📍';
  document.getElementById('pinVisitedAt').value   = pin.visited_at || '';
  openModal('pinModal');
}

function closePinModal() {
  closeModal('pinModal');
  state.addClickMode = false;
}

async function savePin(e) {
  e.preventDefault();
  const data = {
    title:       document.getElementById('pinTitle').value.trim(),
    description: document.getElementById('pinDescription').value.trim(),
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
    updateStats();
    closePinModal();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function deletePinById(id) {
  const pin = state.pins.find(p => p.id === id);
  if (!pin) return;
  if (!confirm(`Remove "${pin.title}" from your map?`)) return;
  try {
    await deletePin(id);
    state.pins = state.pins.filter(p => p.id !== id);
    removeMarkerFromMap(id);
    renderSidebarPinList(state.pins);
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
function showImportModal() { openModal('importModal'); }
function closeImportModal() { closeModal('importModal'); }

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
  el.addEventListener('transitionend', () => { el.style.display = 'none'; }, { once: true });
  document.removeEventListener('keydown', handleEscKey);
}

function handleEscKey(e) {
  if (e.key === 'Escape') {
    closePinModal();
    closeImportModal();
  }
}

// Close modal on overlay click
document.querySelectorAll('.modal-overlay').forEach(overlay => {
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay) {
      closePinModal();
      closeImportModal();
    }
  });
});

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

// ─── Kick-off ─────────────────────────────────────────────────
loadPins();
