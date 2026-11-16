import { store, useStore } from '../state/store';
import { Modal } from './Dialog';
import { restartTour } from './Tour';

export function HelpDialog() {
  const open = useStore((s) => s.helpOpen);
  if (!open) return null;
  const close = () => store.set({ helpOpen: false });
  return (
    <Modal title="How to read the console" onClose={close} labelledBy="help-title">
      <div className="help">
        <h3>The airspace</h3>
        <ul className="legend-list">
          <li>
            <span className="swatch allowed" aria-hidden="true" />
            <span>A cyan flight is a call that was allowed.</span>
          </li>
          <li>
            <span className="swatch blocked" aria-hidden="true" />
            <span>
              A red flight stops mid-air: Gjallarhorn blocked it. Click its <strong>✕</strong> marker to open the incident.
            </span>
          </li>
          <li>
            <span className="swatch flagged" aria-hidden="true" />
            <span>An amber flight would have been blocked, but Gjallarhorn is in monitor-only mode, so it ran.</span>
          </li>
          <li>
            <span className="swatch external" aria-hidden="true" />
            <span>An orange ring marks an outside party, such as the customer chat.</span>
          </li>
          <li>
            <span className="swatch locked" aria-hidden="true" />
            <span>A red shield means the agent is locked: every call it makes, or that passes through it, is refused.</span>
          </li>
        </ul>
        <p>Columns run left to right like a chain: people and apps start work, agents pass it along, and tools and models do it.</p>
        <h3>Keyboard</h3>
        <dl className="keys">
          <dt>
            <kbd>1</kbd> <kbd>2</kbd> <kbd>3</kbd>
          </dt>
          <dd>Airspace, table, policy</dd>
          <dt>
            <kbd>Tab</kbd>
          </dt>
          <dd>Moves through nodes and markers in the airspace, too</dd>
          <dt>
            <kbd>Enter</kbd>
          </dt>
          <dd>Opens the focused node, marker or table row</dd>
          <dt>
            <kbd>Esc</kbd>
          </dt>
          <dd>Closes the side panel or a dialog</dd>
          <dt>
            <kbd>?</kbd>
          </dt>
          <dd>This help</dd>
        </dl>
        <p className="muted">Motion follows your system's reduced-motion setting. The “Motion on / off” button in the top bar switches it for this console; when it's off, calls appear without flying.</p>
      </div>
      <div className="modal-actions">
        <button
          className="btn"
          onClick={() => {
            close();
            store.set({ view: 'airspace' });
            restartTour();
          }}
        >
          Show the tour again
        </button>
        <button className="btn primary" onClick={close} data-autofocus>
          Close
        </button>
      </div>
    </Modal>
  );
}
