import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { Link } from "react-router-dom";
import { NavIcon } from "../app/nav-icons.js";

// How a row sizes its cards: posters are 2:3, tiles are wide.
export type MediaRowShape = "poster" | "tile";

type MediaRowProps = Readonly<{
  // The row's heading, also the accessible name of its list.
  title: string;
  shape?: MediaRowShape;
  // Where "See all" leads, or nothing when the row is the whole list.
  seeAll?: string | undefined;
  // Shown in place of the list while it loads, fails, or is empty.
  status?: ReactNode;
  // One <li> per card, plus any trailing control such as "More".
  children?: ReactNode;
  // Below the list, such as a failed "More".
  footer?: ReactNode;
}>;

// One horizontal row of cards, the way Seerr lays out its home page. The
// row is a region named by its heading. Its list scrolls sideways without
// a visible scroll bar, by touch, trackpad, or keyboard focus, and the
// arrow buttons page it one screen at a time. Card widths come from the
// stylesheet so a whole number of cards fills the row at every width.
export function MediaRow({
  title,
  shape = "poster",
  seeAll,
  status,
  children,
  footer,
}: MediaRowProps): ReactNode {
  const id = useId();
  const list = useRef<HTMLUListElement>(null);
  const showList = status === undefined;
  const edges = useScrollEdges(list, showList);
  return (
    <section aria-labelledby={id} className="media-row">
      <div className="media-row-head">
        <h2 id={id}>{title}</h2>
        {seeAll === undefined ? null : (
          <Link to={seeAll} className="media-row-all">
            See all<span className="visually-hidden"> {title}</span>
          </Link>
        )}
        {showList ? (
          <div className="media-row-arrows">
            <ArrowButton
              direction="back"
              title={title}
              disabled={!edges.back}
              onClick={() => {
                pageRow(list.current, -1);
              }}
            />
            <ArrowButton
              direction="forward"
              title={title}
              disabled={!edges.forward}
              onClick={() => {
                pageRow(list.current, 1);
              }}
            />
          </div>
        ) : null}
      </div>
      {showList ? (
        <div className={`media-row-viewport media-row-${shape}`}>
          <ul ref={list} className="media-row-list" aria-label={title}>
            {children}
          </ul>
        </div>
      ) : (
        status
      )}
      {footer}
    </section>
  );
}

function ArrowButton({
  direction,
  title,
  disabled,
  onClick,
}: Readonly<{
  direction: "back" | "forward";
  title: string;
  disabled: boolean;
  onClick: () => void;
}>): ReactNode {
  return (
    <button
      type="button"
      className="btn-ghost media-row-arrow"
      disabled={disabled}
      onClick={onClick}
    >
      <NavIcon name={direction === "back" ? "chevron-left" : "chevron-right"} />
      <span className="visually-hidden">
        Scroll {title} {direction}
      </span>
    </button>
  );
}

type Edges = Readonly<{ back: boolean; forward: boolean }>;

// A pixel of slack absorbs the fractional scroll positions browsers report
// at non-integer zoom levels.
const EDGE_SLACK = 1;

const NO_EDGES: Edges = { back: false, forward: false };

// Whether the list can scroll back or forward, kept current as it scrolls,
// resizes, or gains cards. The observers report once as they start, so the
// first answer arrives without waiting for a scroll.
function useScrollEdges(
  list: RefObject<HTMLUListElement | null>,
  active: boolean,
): Edges {
  const [edges, setEdges] = useState<Edges>(NO_EDGES);
  useEffect(() => {
    const element = list.current;
    if (!active || element === null) {
      return undefined;
    }
    const update = (): void => {
      const next = readEdges(element);
      setEdges((current) =>
        current.back === next.back && current.forward === next.forward
          ? current
          : next,
      );
    };
    element.addEventListener("scroll", update, { passive: true });
    const resize =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(update);
    resize?.observe(element);
    const added = new MutationObserver(update);
    added.observe(element, { childList: true });
    return () => {
      element.removeEventListener("scroll", update);
      resize?.disconnect();
      added.disconnect();
    };
  }, [list, active]);
  return active ? edges : NO_EDGES;
}

function readEdges(element: HTMLElement): Edges {
  const max = element.scrollWidth - element.clientWidth;
  return {
    back: element.scrollLeft > EDGE_SLACK,
    forward: element.scrollLeft < max - EDGE_SLACK,
  };
}

// Scrolls one visible width, less the gutters, so the card at the edge
// stays in view. Smooth unless the system asks for reduced motion.
function pageRow(element: HTMLUListElement | null, direction: 1 | -1): void {
  if (element === null || typeof element.scrollBy !== "function") {
    return;
  }
  const style = getComputedStyle(element);
  const gutters =
    Number.parseFloat(style.paddingLeft) +
    Number.parseFloat(style.paddingRight);
  const width = Math.max(
    element.clientWidth - gutters,
    element.clientWidth / 2,
  );
  element.scrollBy({
    left: direction * width,
    behavior: prefersReducedMotion() ? "auto" : "smooth",
  });
}

function prefersReducedMotion(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}
