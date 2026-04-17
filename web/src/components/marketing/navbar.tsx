"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { SignedIn, SignedOut } from "@clerk/nextjs";
import { AnimatePresence, motion } from "motion/react";
import { Menu, X } from "lucide-react";
import { ThemeToggle } from "@/components/dashboard/theme-toggle";

/**
 * Navbar — floating pill variant.
 *
 * A centered, rounded-full bar over an amber-glass surface (no full-width
 * banner). Logo sits at 56px so it reads at arm's length; links in the
 * middle; auth + theme toggle on the right. Motion handles the mount-in
 * stagger and the mobile sheet spring.
 *
 * Scroll behaviour is preserved from the prior navbar:
 *   - fades its glass background in after 20px of scroll
 *   - slides up out of view when scrolling down past 100px, slides back on
 *     upward scroll (lastYRef diff)
 *
 * Stays dark-only — the Argus palette has no light variant, so the "white
 * pill" from the donor is swapped for charcoal-with-amber-ring glass.
 */
const navLinks = [
  { href: "/#features", label: "Features" },
  { href: "/#memory", label: "Memory" },
  { href: "/pricing", label: "Pricing" },
  { href: "/compare", label: "Compare" },
  { href: "/changelog", label: "Changelog" },
  { href: "/docs", label: "Docs" },
];

export function Navbar() {
  const [scrolled, setScrolled] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const [pillStyle, setPillStyle] = useState<{
    left: number;
    width: number;
    opacity: number;
  }>({ left: 0, width: 0, opacity: 0 });
  const lastYRef = useRef(0);

  useEffect(() => {
    let ticking = false;
    const onScroll = () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(() => {
        const y = window.scrollY;
        setScrolled(y > 20);
        setHidden(y > 100 && y > lastYRef.current);
        lastYRef.current = y;
        ticking = false;
      });
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  const handleLinkEnter = useCallback(
    (e: React.MouseEvent<HTMLAnchorElement>, idx: number) => {
      const rect = e.currentTarget.getBoundingClientRect();
      const parent = e.currentTarget.parentElement!.getBoundingClientRect();
      setPillStyle({
        left: rect.left - parent.left - 10,
        width: rect.width + 20,
        opacity: 1,
      });
      setHoverIdx(idx);
    },
    [],
  );
  const handleLinkLeave = useCallback(() => {
    setPillStyle((p) => ({ ...p, opacity: 0 }));
    setHoverIdx(null);
  }, []);

  const toggleMenu = useCallback(() => setMenuOpen((v) => !v), []);
  const closeMenu = useCallback(() => setMenuOpen(false), []);

  return (
    <div
      className={`pointer-events-none fixed inset-x-0 top-0 z-50 flex w-full justify-center px-4 transition-transform duration-300 ease-[cubic-bezier(0.23,1,0.32,1)] sm:px-6 ${
        hidden ? "-translate-y-[120%]" : "translate-y-0"
      }`}
    >
      <nav
        aria-label="Primary"
        className={`pointer-events-auto mt-4 flex w-full max-w-5xl items-center justify-between gap-3 rounded-full border px-4 py-2.5 backdrop-blur-xl transition-[background-color,border-color,box-shadow] duration-300 sm:px-5 ${
          scrolled
            ? "border-amber/15 bg-void/80 shadow-[0_12px_40px_-12px_color-mix(in_oklch,var(--color-amber-glow)_22%,transparent),inset_0_1px_0_color-mix(in_oklch,var(--color-amber-glow)_10%,transparent)]"
            : "border-iron/60 bg-charcoal/40 shadow-[inset_0_1px_0_color-mix(in_oklch,var(--color-amber-glow)_8%,transparent)]"
        }`}
      >
        {/* Logo — bumped to h-14 (56px) so it reads as the brand anchor */}
        <Link
          href="/"
          aria-label="Argus home"
          className="group flex shrink-0 items-center pl-1 transition-[filter] duration-200 hover:drop-shadow-[0_0_14px_color-mix(in_oklch,var(--color-amber-glow)_55%,transparent)]"
        >
          <Image
            src="/logo-text.png"
            alt="Argus"
            width={220}
            height={160}
            priority
            sizes="170px"
            className="h-14 w-auto"
          />
        </Link>

        {/* Center links with animated hover pill */}
        <div
          className="relative hidden items-center md:flex"
          onMouseLeave={handleLinkLeave}
        >
          <div
            aria-hidden
            className="pointer-events-none absolute top-1/2 h-8 -translate-y-1/2 rounded-full bg-iron/50 transition-all duration-200 ease-out"
            style={{
              left: pillStyle.left,
              width: pillStyle.width,
              opacity: pillStyle.opacity,
            }}
          />
          {navLinks.map((link, idx) => (
            <motion.div
              key={link.href}
              initial={{ opacity: 0, y: -6 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.25, delay: idx * 0.03 }}
            >
              <Link
                href={link.href}
                onMouseEnter={(e) => handleLinkEnter(e, idx)}
                className={`relative z-10 block px-3 py-1.5 font-mono text-[12px] tracking-[0.02em] transition-colors duration-200 ${
                  hoverIdx === idx ? "text-foreground" : "text-slate-text hover:text-ash"
                }`}
              >
                {link.label}
              </Link>
            </motion.div>
          ))}
        </div>

        {/* Right cluster */}
        <div className="hidden items-center gap-3 pr-1 md:flex">
          <ThemeToggle />
          <SignedOut>
            <Link
              href="/sign-in"
              className="font-mono text-[12px] text-slate-text transition-colors hover:text-foreground"
            >
              Sign in
            </Link>
            <Link
              href="/sign-up"
              className="inline-flex items-center rounded-full bg-amber px-5 py-2 font-mono text-[12px] font-semibold text-primary-foreground transition-[transform,background-color] duration-150 hover:scale-[1.03] hover:bg-amber-glow"
            >
              Get started
            </Link>
          </SignedOut>
          <SignedIn>
            <Link
              href="/dashboard"
              className="inline-flex items-center rounded-full bg-amber px-5 py-2 font-mono text-[12px] font-semibold text-primary-foreground transition-[transform,background-color] duration-150 hover:scale-[1.03] hover:bg-amber-glow"
            >
              Dashboard
            </Link>
          </SignedIn>
        </div>

        {/* Mobile hamburger */}
        <button
          type="button"
          onClick={toggleMenu}
          aria-label={menuOpen ? "Close menu" : "Open menu"}
          aria-expanded={menuOpen}
          className="inline-flex items-center justify-center rounded-full border border-iron/70 p-2 text-slate-text transition-colors hover:border-amber/40 hover:text-amber md:hidden"
        >
          {menuOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
        </button>
      </nav>

      {/* Mobile sheet — slides from the right, spring damped */}
      <AnimatePresence>
        {menuOpen && (
          <motion.div
            key="mobile-sheet"
            className="pointer-events-auto fixed inset-0 z-40 flex flex-col gap-6 bg-background/95 px-6 pb-8 pt-24 backdrop-blur-xl md:hidden"
            initial={{ opacity: 0, x: "100%" }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: "100%" }}
            transition={{ type: "spring", damping: 26, stiffness: 280 }}
          >
            <div className="flex flex-col gap-1">
              {navLinks.map((link, i) => (
                <motion.div
                  key={link.href}
                  initial={{ opacity: 0, x: 16 }}
                  animate={{ opacity: 1, x: 0 }}
                  transition={{ delay: i * 0.05 + 0.08 }}
                >
                  <Link
                    href={link.href}
                    onClick={closeMenu}
                    className="block border-b border-iron/40 py-3 font-mono text-[15px] text-foreground"
                  >
                    {link.label}
                  </Link>
                </motion.div>
              ))}
            </div>

            <div className="flex items-center justify-between pt-2">
              <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-slate-text">
                Theme
              </span>
              <ThemeToggle />
            </div>

            <div className="mt-auto flex flex-col gap-3 pt-4">
              <SignedOut>
                <Link
                  href="/sign-in"
                  onClick={closeMenu}
                  className="inline-flex items-center justify-center rounded-full border border-iron py-3 font-mono text-[13px] text-foreground transition-colors hover:border-amber/50"
                >
                  Sign in
                </Link>
                <Link
                  href="/sign-up"
                  onClick={closeMenu}
                  className="inline-flex items-center justify-center rounded-full bg-amber py-3 font-mono text-[13px] font-semibold text-primary-foreground"
                >
                  Get started
                </Link>
              </SignedOut>
              <SignedIn>
                <Link
                  href="/dashboard"
                  onClick={closeMenu}
                  className="inline-flex items-center justify-center rounded-full bg-amber py-3 font-mono text-[13px] font-semibold text-primary-foreground"
                >
                  Dashboard
                </Link>
              </SignedIn>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
