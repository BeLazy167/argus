"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { SignedIn, SignedOut } from "@clerk/nextjs";
import { AnimatePresence, motion } from "motion/react";
import { Menu, X } from "lucide-react";
import { ThemeToggle } from "@/components/dashboard/theme-toggle";

/**
 * Navbar — floating pill with shared-layout hover indicator.
 *
 * The hover pill uses Motion's `layoutId` so moving between links animates
 * a single element through the layout tree rather than mutating width/left
 * on a position-absolute box. This is the Linear / Vercel / Emil pattern:
 *   - compositor-thread transforms only (no layout on hover)
 *   - spring-based interpolation between link positions — no jank, no
 *     manual getBoundingClientRect, no derived state
 *   - the same element "morphs" between links so clicks never land on a
 *     mid-transition phantom
 *
 * Press feedback via `whileTap={{ scale: 0.97 }}` on interactive elements
 * gives a physical click response without hurting perceived latency.
 *
 * Stays dark-only — Argus has no light palette, so the donor "white pill"
 * is swapped for amber-ringed charcoal glass.
 *
 * Scroll behaviour preserved: fades the glass background in after 20px of
 * scroll, slides up out of view when scrolling down past 100px.
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
  const [hoveredHref, setHoveredHref] = useState<string | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
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
        {/* Logo — 56px reads as the brand anchor, drop-shadow on hover */}
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

        {/* Center links with shared-layout hover pill */}
        <ul
          className="hidden items-center md:flex"
          onMouseLeave={() => setHoveredHref(null)}
        >
          {navLinks.map((link) => {
            const active = hoveredHref === link.href;
            return (
              <li key={link.href} className="relative">
                <Link
                  href={link.href}
                  onMouseEnter={() => setHoveredHref(link.href)}
                  onFocus={() => setHoveredHref(link.href)}
                  onBlur={() => setHoveredHref((h) => (h === link.href ? null : h))}
                  className={`relative block rounded-full px-3.5 py-1.5 font-mono text-[12px] tracking-[0.02em] transition-colors duration-150 focus:outline-none focus-visible:ring-1 focus-visible:ring-amber/50 ${
                    active ? "text-foreground" : "text-slate-text hover:text-ash"
                  }`}
                >
                  {active && (
                    <motion.span
                      layoutId="nav-pill"
                      aria-hidden="true"
                      className="absolute inset-0 -z-10 rounded-full bg-iron/60"
                      transition={{ type: "spring", stiffness: 420, damping: 32, mass: 0.6 }}
                    />
                  )}
                  <span className="relative">{link.label}</span>
                </Link>
              </li>
            );
          })}
        </ul>

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
            <motion.div whileTap={{ scale: 0.96 }}>
              <Link
                href="/sign-up"
                className="inline-flex items-center rounded-full bg-amber px-5 py-2 font-mono text-[12px] font-semibold text-primary-foreground transition-colors duration-150 hover:bg-amber-glow"
              >
                Get started
              </Link>
            </motion.div>
          </SignedOut>
          <SignedIn>
            <motion.div whileTap={{ scale: 0.96 }}>
              <Link
                href="/dashboard"
                className="inline-flex items-center rounded-full bg-amber px-5 py-2 font-mono text-[12px] font-semibold text-primary-foreground transition-colors duration-150 hover:bg-amber-glow"
              >
                Dashboard
              </Link>
            </motion.div>
          </SignedIn>
        </div>

        {/* Mobile hamburger */}
        <motion.button
          type="button"
          onClick={() => setMenuOpen((v) => !v)}
          whileTap={{ scale: 0.92 }}
          aria-label={menuOpen ? "Close menu" : "Open menu"}
          aria-expanded={menuOpen}
          className="inline-flex min-h-11 min-w-11 items-center justify-center rounded-full border border-iron/70 p-2 text-slate-text transition-colors hover:border-amber/40 hover:text-amber md:hidden"
        >
          {menuOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
        </motion.button>
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
