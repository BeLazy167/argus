"use client";

import { useState, useEffect, useCallback, useRef } from "react";
import Link from "next/link";
import { SignedIn, SignedOut } from "@clerk/nextjs";
import { Menu, X } from "lucide-react";

const navLinks = [
  { href: "/pricing", label: "Pricing" },
  { href: "/docs", label: "Docs" },
  { href: "/blog", label: "Blog" },
];

export function Navbar() {
  const [scrolled, setScrolled] = useState(false);
  const [hidden, setHidden] = useState(false);
  const lastYRef = useRef(0);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [pillStyle, setPillStyle] = useState<{
    left: number;
    width: number;
    opacity: number;
  }>({ left: 0, width: 0, opacity: 0 });

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

  const handleMouseEnter = useCallback(
    (e: React.MouseEvent<HTMLAnchorElement>, idx: number) => {
      const rect = e.currentTarget.getBoundingClientRect();
      const parent = e.currentTarget.parentElement!.getBoundingClientRect();
      setPillStyle({
        left: rect.left - parent.left - 8,
        width: rect.width + 16,
        opacity: 1,
      });
      setHoverIdx(idx);
    },
    [],
  );

  const handleMouseLeave = useCallback(() => {
    setPillStyle((p) => ({ ...p, opacity: 0 }));
    setHoverIdx(null);
  }, []);

  return (
    <nav
      className={`fixed top-0 z-50 w-full transition-[transform,background-color,border-color,box-shadow] duration-300 ease-[cubic-bezier(0.23,1,0.32,1)] ${
        hidden ? "-translate-y-full" : "translate-y-0"
      } ${
        scrolled
          ? "border-b border-amber/10 bg-void/90 backdrop-blur-xl shadow-[0_1px_24px_-8px_oklch(0.77_0.15_75/0.08)]"
          : "border-b border-transparent bg-transparent"
      }`}
    >
      {/* Ambient top line */}
      <div
        className={`absolute inset-x-0 top-0 h-px transition-opacity duration-300 ${
          scrolled ? "opacity-100" : "opacity-0"
        }`}
        style={{
          background:
            "linear-gradient(90deg, transparent 0%, oklch(0.77 0.15 75 / 0.4) 50%, transparent 100%)",
        }}
      />

      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-6 relative">
        {/* Logo */}
        <Link
          href="/"
          className="group flex items-center gap-2.5"
        >
          {/* Mini eye icon */}
          <svg
            viewBox="0 0 24 12"
            fill="none"
            className="h-3 w-6 text-amber transition-[filter] duration-200 group-hover:drop-shadow-[0_0_6px_oklch(0.77_0.15_75/0.5)]"
          >
            <path
              d="M2 6C2 6 6 1.5 12 1.5C18 1.5 22 6 22 6C22 6 18 10.5 12 10.5C6 10.5 2 6 2 6Z"
              stroke="currentColor"
              strokeWidth="1.2"
              fill="none"
            />
            <circle cx="12" cy="6" r="2.5" fill="currentColor" />
          </svg>
          <span className="wordmark text-sm text-amber tracking-[0.2em] transition-[text-shadow] duration-200 group-hover:text-shadow-[0_0_12px_oklch(0.77_0.15_75/0.4)]">
            ARGUS
          </span>
        </Link>

        {/* Center nav links with sliding pill */}
        <div
          className="hidden md:flex items-center gap-1 relative"
          onMouseLeave={handleMouseLeave}
        >
          {/* Hover pill */}
          <div
            className="absolute top-1/2 -translate-y-1/2 h-8 rounded-md bg-iron/60 transition-all duration-200 ease-out pointer-events-none"
            style={{
              left: pillStyle.left,
              width: pillStyle.width,
              opacity: pillStyle.opacity,
            }}
          />

          {navLinks.map((link, idx) => (
            <Link
              key={link.href}
              href={link.href}
              className={`relative z-10 px-3 py-1.5 text-xs font-mono transition-colors duration-200 ${
                hoverIdx === idx ? "text-foreground" : "text-slate-text hover:text-ash"
              }`}
              onMouseEnter={(e) => handleMouseEnter(e, idx)}
            >
              {link.label}
            </Link>
          ))}
        </div>

        {/* Mobile hamburger */}
        <button
          onClick={() => setMenuOpen(!menuOpen)}
          className="p-2 md:hidden"
          aria-label={menuOpen ? "Close menu" : "Open menu"}
        >
          {menuOpen ? <X className="h-5 w-5 text-zinc-400" /> : <Menu className="h-5 w-5 text-zinc-400" />}
        </button>

        {/* Right side: auth */}
        <div className="flex items-center gap-3">
          <SignedOut>
            <Link
              href="/sign-in"
              className="text-xs font-mono text-slate-text hover:text-foreground transition-colors duration-200"
            >
              Sign in
            </Link>
            <Link
              href="/sign-up"
              className="group relative inline-flex h-8 items-center rounded-md bg-amber px-5 text-xs font-mono font-medium text-void transition-[transform,box-shadow] duration-200 ease-out hover:shadow-[0_0_20px_-4px_oklch(0.77_0.15_75/0.5)] active:scale-[0.97]"
            >
              <span className="relative z-10">Get started</span>
              <div className="absolute inset-0 rounded-md bg-gradient-to-r from-amber to-[oklch(0.82_0.14_80)] opacity-0 transition-opacity duration-200 group-hover:opacity-100" />
            </Link>
          </SignedOut>
          <SignedIn>
            <Link
              href="/dashboard"
              className="group relative inline-flex h-8 items-center rounded-md bg-amber px-5 text-xs font-mono font-medium text-void transition-[transform,box-shadow] duration-200 ease-out hover:shadow-[0_0_20px_-4px_oklch(0.77_0.15_75/0.5)] active:scale-[0.97]"
            >
              <span className="relative z-10">Dashboard</span>
              <div className="absolute inset-0 rounded-md bg-gradient-to-r from-amber to-[oklch(0.82_0.14_80)] opacity-0 transition-opacity duration-200 group-hover:opacity-100" />
            </Link>
          </SignedIn>
        </div>
      </div>

      {/* Mobile dropdown */}
      {menuOpen && (
        <div className="border-t border-zinc-800 bg-void/95 backdrop-blur-sm md:hidden">
          <div className="flex flex-col gap-1 px-6 py-4">
            {navLinks.map((link) => (
              <Link
                key={link.href}
                href={link.href}
                onClick={() => setMenuOpen(false)}
                className="py-2 text-xs font-mono text-slate-text hover:text-foreground transition-colors"
              >
                {link.label}
              </Link>
            ))}
          </div>
        </div>
      )}
    </nav>
  );
}
