import { useEffect, useRef } from "react";
import logoUrl from "../assets/tunnex-logo.svg";

/**
 * AuthMesh — the login hero, TRANSCRIBED from the wireframe rather than re-imagined.
 *
 * ⛔ THE FIRST ATTEMPT WAS A THUMBNAIL OF THE DESIGN, NOT THE DESIGN. It drew six plain circles at
 * 300x264 with monospace labels, no provider marks, no animation, and put the hero beside the form
 * instead of behind it. The design's own SVG was sitting in the handoff file the whole time.
 *
 * > **WHEN THE SOURCE SHIPS THE ARTEFACT, TRANSCRIBE IT. Re-deriving a picture from a screenshot
 * > reproduces what you noticed about it, which is never the whole of it.**
 *
 * viewBox 0 0 480 300, hub at (240,150) — the design's coordinates, unscaled. `preserveAspectRatio`
 * does the fitting, so the geometry cannot be stretched by a container the way S14.7's flow graph
 * was when a viewBox met `w-full`.
 *
 * The packet dots are driven in JS along their edges because SMIL is unreliable in the Electron
 * renderer and `offset-path` has no Safari/older-Chromium story; a rAF loop is the portable option
 * and is cancelled on unmount.
 */
export function AuthMesh() {
  const ref = useRef<SVGSVGElement | null>(null);

  useEffect(() => {
    const svg = ref.current;
    if (!svg) return;
    // ⛔ RESPECT reduced-motion. The mesh is decorative; a user who asked for stillness gets the
    // static picture, and the CSS animations are suppressed by the media query in index.css.
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;

    const pkts = Array.from(svg.querySelectorAll<SVGCircleElement>(".tnx-pkt"));
    const edges = Array.from(svg.querySelectorAll<SVGPathElement | SVGLineElement>(".tnx-edge"));
    if (pkts.length === 0 || edges.length === 0) return;

    let raf = 0;
    const start = performance.now();
    const tick = (now: number) => {
      const t = (now - start) / 1000;
      pkts.forEach((p, i) => {
        const edge = edges[i % edges.length];
        const len = (edge as SVGGeometryElement).getTotalLength?.() ?? 0;
        if (!len) return;
        // Each packet runs its edge on its own phase so they never march in lockstep.
        const u = ((t * 0.24 + i * 0.17) % 1) * len;
        const pt = (edge as SVGGeometryElement).getPointAtLength(u);
        p.setAttribute("cx", String(pt.x));
        p.setAttribute("cy", String(pt.y));
      });
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, []);

  // ⛔ AN INFERENCE, LABELLED AS ONE. `.tnx-afloat` / `.tnx-afloat2` are DEFINED in the handoff's
  // CSS and applied to NOTHING — dead rules in the source. The names ("auth float", two phases
  // .6s apart) say plainly what they were written for, so the node clusters carry them on
  // alternating phases. Recorded as a decision rather than passed off as transcription: the rest
  // of this file is the designer's markup verbatim, and this line is not.
  return (
    <div aria-hidden="true" className="pointer-events-none h-full w-full">
      <svg ref={ref} id="tnxMesh" viewBox="0 0 480 300" preserveAspectRatio="xMidYMid meet" style={{ width: "100%", height: "100%", overflow: "visible" }}>
      <defs>
      <radialGradient id="hubGlow" cx="50%" cy="50%" r="50%"><stop offset="0%" stopColor="#8A8A86" stopOpacity=".55" /><stop offset="60%" stopColor="#C9C9C4" stopOpacity=".14" /><stop offset="100%" stopColor="#C9C9C4" stopOpacity="0" /></radialGradient>
      <linearGradient id="spoke" x1="0" y1="0" x2="1" y2="0"><stop offset="0%" stopColor="#C9C9C4" stopOpacity=".15" /><stop offset="100%" stopColor="#CFCFCA" stopOpacity=".85" /></linearGradient>
      <clipPath id="hubClip"><rect x="222" y="132" width="36" height="36" rx="9" /></clipPath>
      </defs>
      <circle cx="240" cy="150" r="118" fill="url(#hubGlow)" className="tnx-aglow" />
      {/* spokes */}
      <g fill="none" stroke="url(#spoke)" strokeWidth="1.3">
      <path id="tnxSp0" d="M72 44 Q 150 108 240 150" className="tnx-edge" />
      <path id="tnxSp1" d="M408 44 Q 330 108 240 150" className="tnx-edge" style={{ animationDelay: "-.5s" }} />
      <path id="tnxSp2" d="M56 150 Q 150 138 240 150" className="tnx-edge" style={{ animationDelay: "-1s" }} />
      <path id="tnxSp3" d="M424 150 Q 330 162 240 150" className="tnx-edge" style={{ animationDelay: "-1.5s" }} />
      <path id="tnxSp4" d="M92 256 Q 160 202 240 150" className="tnx-edge" style={{ animationDelay: "-.8s" }} />
      <path id="tnxSp5" d="M388 256 Q 320 202 240 150" className="tnx-edge" style={{ animationDelay: "-.2s" }} />
      </g>
      {/* packets converging on hub (GSAP-driven) */}
      <g fill="#E6E6E2">
      <circle className="tnx-pkt" data-sp="0" r="2.6" cx="72" cy="44" />
      <circle className="tnx-pkt" data-sp="1" r="2.6" cx="408" cy="44" />
      <circle className="tnx-pkt" data-sp="2" r="2.6" cx="56" cy="150" />
      <circle className="tnx-pkt" data-sp="3" r="2.6" cx="424" cy="150" />
      <circle className="tnx-pkt" data-sp="4" r="2.6" cx="92" cy="256" />
      <circle className="tnx-pkt" data-sp="5" r="2.6" cx="388" cy="256" />
      </g>
      {/* hub */}
      <circle cx="240" cy="150" r="46" fill="none" stroke="#C9C9C4" strokeWidth="1" strokeDasharray="3 7" opacity=".5" className="tnx-orbit" />
      <circle cx="240" cy="150" r="30" fill="none" stroke="#8A8A86" strokeWidth="1" className="tnx-aring" />
      <circle cx="240" cy="150" r="30" fill="none" stroke="#C9C9C4" strokeWidth="1" className="tnx-aring2" />
      {/* ⛔ THE HUB MARK. The design puts the Tunnex tile at the centre of the mesh — the whole
          picture is "everything joins HERE", and without it the hub was an anonymous dot. The
          wireframe layers it as HTML over the SVG; embedded here instead so it scales with the
          viewBox and cannot drift from the rings at any container size. */}
      <image
        href={logoUrl}
        x="222"
        y="132"
        width="36"
        height="36"
        clipPath="url(#hubClip)"
        preserveAspectRatio="xMidYMid slice"
      />
      {/* nodes */}
      <g className="tnx-node tnx-afloat"><circle cx="41" cy="40" r="15" fill="rgba(26,26,26,.95)" stroke="rgba(255,255,255,0.14)" strokeWidth="1" /><g transform="translate(33,35)"><path d="M0 5 Q 8 11 16 5" fill="none" stroke="#FF9900" strokeWidth="2.4" strokeLinecap="round" /><path d="M12.5 3.6 L17.4 5 L13 8.2 Z" fill="#FF9900" /></g><text x="58" y="49" fill="#E4E4E1" fontFamily="Instrument Sans" fontSize="12" fontWeight="600">AWS VPC</text></g>
      <g className="tnx-node tnx-afloat2"><circle cx="378" cy="43" r="15" fill="rgba(26,26,26,.95)" stroke="rgba(255,255,255,0.14)" strokeWidth="1" /><g transform="translate(370,35) scale(.62)"><path fill="#35C1F1" d="M5.483 21.3H24L14.025 4.013l-3.038 8.347 5.836 6.938L5.483 21.3z" /><path fill="#0078D4" d="M13.23 2.7L6.98 7.98 0 19.966h5.626z" /></g><text x="394" y="49" fill="#E4E4E1" fontFamily="Instrument Sans" fontSize="12" fontWeight="600">Azure</text></g>
      <g className="tnx-node tnx-afloat"><circle cx="28" cy="148" r="15" fill="rgba(26,26,26,.95)" stroke="rgba(255,255,255,0.14)" strokeWidth="1" /><g transform="translate(20,141)" fill="none" stroke="#CFCFCA" strokeWidth="1.4"><rect x="0" y="0" width="15" height="5" rx="1.5" /><rect x="0" y="8" width="15" height="5" rx="1.5" /><circle cx="3" cy="2.5" r=".6" fill="#CFCFCA" /><circle cx="3" cy="10.5" r=".6" fill="#CFCFCA" /></g><text x="44" y="155" fill="#E4E4E1" fontFamily="Instrument Sans" fontSize="12" fontWeight="600">On-prem</text></g>
      <g className="tnx-node tnx-afloat2"><circle cx="392" cy="149" r="15" fill="rgba(26,26,26,.95)" stroke="rgba(255,255,255,0.14)" strokeWidth="1" /><g transform="translate(384,141) scale(.335)"><path fill="#EA4335" d="M24 9.5c3.5 0 6.6 1.2 9 3.6l6.7-6.7C35.6 2.4 30.1 0 24 0 14.6 0 6.4 5.4 2.5 13.3l7.9 6.1C12.2 13.6 17.6 9.5 24 9.5z" /><path fill="#4285F4" d="M46.5 24.5c0-1.6-.1-3.1-.4-4.5H24v9h12.7c-.5 3-2.2 5.5-4.7 7.2l7.3 5.7c4.3-3.9 6.7-9.7 6.7-16.4z" /><path fill="#FBBC05" d="M10.4 28.6c-.5-1.5-.8-3-.8-4.6s.3-3.1.8-4.6l-7.9-6.1C.9 16.5 0 20.1 0 24s.9 7.5 2.5 10.7l7.9-6.1z" /><path fill="#34A853" d="M24 48c6.1 0 11.3-2 15-5.5l-7.3-5.7c-2 1.4-4.6 2.2-7.7 2.2-6.4 0-11.8-4.1-13.6-9.9l-7.9 6.1C6.4 42.6 14.6 48 24 48z" /></g><text x="408" y="155" fill="#E4E4E1" fontFamily="Instrument Sans" fontSize="12" fontWeight="600">GCP</text></g>
      <g className="tnx-node tnx-afloat"><circle cx="62" cy="256" r="15" fill="rgba(26,26,26,.95)" stroke="rgba(255,255,255,0.14)" strokeWidth="1" /><g transform="translate(54,249)" fill="none" stroke="#A9A9A6" strokeWidth="1.5" strokeLinejoin="round"><polygon points="8,0 14,3.6 14,10.4 8,14 2,10.4 2,3.6" /><line x1="8" y1="4" x2="8" y2="10" /><line x1="5" y1="6" x2="11" y2="6" /></g><text x="78" y="261" fill="#E4E4E1" fontFamily="Instrument Sans" fontSize="12" fontWeight="600">Kubernetes</text></g>
      <g className="tnx-node tnx-afloat2"><circle cx="358" cy="256" r="15" fill="rgba(26,26,26,.95)" stroke="rgba(255,255,255,0.14)" strokeWidth="1" /><g transform="translate(350,248)" fill="none" stroke="#CFCFCA" strokeWidth="1.4" strokeLinejoin="round"><rect x="0" y="0" width="15" height="10" rx="1.5" /><line x1="5" y1="14" x2="10" y2="14" /><line x1="7.5" y1="10" x2="7.5" y2="14" /></g><text x="374" y="261" fill="#E4E4E1" fontFamily="Instrument Sans" fontSize="12" fontWeight="600">Remote</text></g>
      </svg>
    </div>
  );
}
