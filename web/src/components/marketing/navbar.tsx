"use client";

import { useState, useEffect, useCallback, useRef } from "react";
import Image from "next/image";
import Link from "next/link";
import { SignedIn, SignedOut } from "@clerk/nextjs";
import { Menu, X } from "lucide-react";

const navLinks = [
  { href: "/pricing", label: "Pricing" },
  { href: "/compare", label: "Compare" },
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
          ? "border-b border-amber/10 bg-void/90 backdrop-blur-xl shadow-[0_1px_24px_-8px_color-mix(in_oklch,var(--color-amber-glow)_8%,transparent)]"
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
            "linear-gradient(90deg, transparent 0%, color-mix(in oklch, var(--color-amber-glow) 40%, transparent) 50%, transparent 100%)",
        }}
      />

      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-6 relative">
        {/* Logo — Next/Image auto-optimizes (webp/avif, responsive srcset) */}
        <Link
          href="/"
          aria-label="Argus home"
          className="group flex items-center transition-[filter] duration-200 group-hover:drop-shadow-[0_0_12px_color-mix(in_oklch,var(--color-amber-glow)_40%,transparent)]"
        >
          <Image
            src="/logo-text.png"
            alt="Argus"
            width={138}
            height={100}
            priority
            sizes="110px"
            className="h-11 w-auto"
          />
        </Link>

        {/* Center nav links with sliding pill */}
        <div
          className="hidden md:flex items-center gap-1 relative"
          onMouseLeave={handleMouseLeave}
        >
          {/* Hover pill */}
          <div
            className="absolute top-1/2 -translate-y-1/2 h-8 bg-iron/60 transition-all duration-200 ease-out pointer-events-none"
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
        <div className="hidden md:flex items-center gap-3">
          <SignedOut>
            <Link
              href="/sign-in"
              className="text-xs font-mono text-slate-text hover:text-foreground transition-colors"
            >
              Sign in
            </Link>
            <Link
              href="/sign-up"
              className="bg-amber text-background font-mono text-xs font-medium px-4 py-2 hover:bg-amber/90 transition-colors"
            >
              Get started
            </Link>
          </SignedOut>
          <SignedIn>
            <Link
              href="/dashboard"
              className="bg-amber text-background font-mono text-xs font-medium px-4 py-2 hover:bg-amber/90 transition-colors"
            >
              Dashboard
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
            <div className="my-2 border-t border-zinc-800" />
            <SignedOut>
              <Link
                href="/sign-in"
                onClick={() => setMenuOpen(false)}
                className="py-2 text-xs font-mono text-slate-text hover:text-foreground transition-colors"
              >
                Sign in
              </Link>
              <Link
                href="/sign-up"
                onClick={() => setMenuOpen(false)}
                className="inline-flex items-center justify-center bg-amber px-4 py-2 text-xs font-mono font-medium text-void"
              >
                Get started
              </Link>
            </SignedOut>
            <SignedIn>
              <Link
                href="/dashboard"
                onClick={() => setMenuOpen(false)}
                className="inline-flex items-center justify-center bg-amber px-4 py-2 text-xs font-mono font-medium text-void"
              >
                Dashboard
              </Link>
            </SignedIn>
          </div>
        </div>
      )}
    </nav>
  );
}
