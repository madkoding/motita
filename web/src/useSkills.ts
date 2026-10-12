// The skill library: the documents the agent wrote for itself, the ones it archived and the ones
// the background review proposed, and what a person can do to each of them.
import { useCallback, useEffect, useState } from "preact/hooks";
import { choose } from "./choose";
import { filterSkills, patchNamed } from "./appFormat";
import type { Api } from "./prApi";
import type { SetStatus, SkillInfo } from "./appTypes";
import { JSON_HEADERS, PROPOSED_SKILLS_PATH, SKILLS_PATH } from "./appConst";

// errorOf reads the gateway's own words for a refusal, or nothing.
async function errorOf(res: Response): Promise<string | undefined> {
  const err = await res.json().catch(() => ({}));
  return err.error;
}

export function useSkills(api: Api, sessionId: string, setState: SetStatus) {
  const [showSkillLibrary, setShowSkillLibrary] = useState(false);
  // The library browser's state. The list is fetched when the modal OPENS and not on every
  // render: refetching forty documents while the user types in the filter would spend the
  // network on a filter that runs locally.
  const [skills, setSkills] = useState<SkillInfo[]>([]);
  const [archivedSkills, setArchivedSkills] = useState<string[]>([]);
  // Skills the background review proposed: no session reads one until the user accepts it.
  const [proposedSkills, setProposedSkills] = useState<string[]>([]);
  const [proposalOpen, setProposalOpen] = useState<{
    name: string;
    body: string;
  } | null>(null);
  const [skillQuery, setSkillQuery] = useState("");
  const [skillOpen, setSkillOpen] = useState<SkillInfo | null>(null);
  const [skillBody, setSkillBody] = useState("");
  const [skillsBusy, setSkillsBusy] = useState(false);

  // fetchSkills loads the index and the archive together: they are two halves of one answer
  // to "what is in the library", and a browser that showed one without the other would hide
  // the very skills a user opens it to recover.
  //
  // The index is asked FOR THIS SESSION when there is one, and that is not a detail: a library
  // is scoped to a project, so a window opened inside a project must draw that project's shelf.
  // The process-wide list would show the shared documents and hide the project's own — the one
  // thing the window exists to display. Falls back to the process-wide list when no session has
  // been opened yet, which is the state the window starts in.
  const fetchSkills = useCallback(async () => {
    setSkillsBusy(true);
    const indexUrl = choose(
      sessionId,
      `/v1/sessions/${encodeURIComponent(sessionId)}/skills`,
      "/v1/skills",
    );
    try {
      const [res, arch, prop] = await Promise.all([
        api(indexUrl),
        api("/v1/skills/archived"),
        api("/v1/skills/proposed"),
      ]);
      if (!res.ok) {
        setState("could not load the skill library", true);
        setSkillsBusy(false);
        return;
      }
      const data = await res.json();
      setSkills(data.skills || []);
      if (arch.ok) {
        const a = await arch.json();
        setArchivedSkills(a.skills || []);
      }
      if (prop.ok) {
        const p = await prop.json();
        setProposedSkills(p.skills || []);
      }
    } catch {
      setState("could not load the skill library", true);
    }
    setSkillsBusy(false);
  }, [sessionId]);

  // openSkill fetches one document's body. The index deliberately does not carry it.
  const openSkill = useCallback(async (skill: SkillInfo) => {
    try {
      const res = await api(SKILLS_PATH + encodeURIComponent(skill.name));
      if (!res.ok) {
        setState((await errorOf(res)) || "could not open the skill", true);
        return;
      }
      const data = await res.json();
      setSkillBody(data.body || "");
      setSkillOpen(skill);
    } catch {
      setState("could not open the skill", true);
    }
  }, []);

  // pinSkill changes the exemption. The row is patched locally rather than refetched: the
  // answer is one boolean and the list is already on screen.
  const pinSkill = useCallback(async (name: string, pinned: boolean) => {
    try {
      const res = await api(SKILLS_PATH + encodeURIComponent(name) + "/pin", {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({ pinned }),
      });
      if (!res.ok) {
        setState("could not change the pin", true);
        return;
      }
      setSkills((prev) => prev.map((s) => patchNamed(s, name, { pinned })));
      // The open document's own copy has to follow, or the button would flip the pin and
      // keep painting the state it had when the document was opened.
      setSkillOpen((prev) => prev && patchNamed(prev, name, { pinned }));
    } catch {
      setState("could not change the pin", true);
    }
  }, []);

  // openProposal shows a proposed skill, or hides it when it is the one already shown: the user
  // reads what the review wrote before deciding.
  const openProposal = useCallback(
    async (name: string) => {
      if (proposalOpen?.name === name) {
        setProposalOpen(null);
        return;
      }
      try {
        const res = await api(PROPOSED_SKILLS_PATH + encodeURIComponent(name));
        if (!res.ok) {
          setState(
            (await errorOf(res)) || "could not open the proposed skill",
            true,
          );
          return;
        }
        const data = await res.json();
        setProposalOpen({ name, body: data.body || "" });
      } catch {
        setState("could not open the proposed skill", true);
      }
    },
    [proposalOpen],
  );

  // decideProposal accepts a proposed skill into the library or rejects it, then RELOADS the
  // lists: a file moved or left, and the browser must not keep a guess about it.
  const decideProposal = useCallback(
    async (name: string, accept: boolean) => {
      const what = choose(
        accept,
        "could not accept the skill",
        "could not reject the skill",
      );
      try {
        let res: Response;
        if (accept) {
          res = await api(
            PROPOSED_SKILLS_PATH + encodeURIComponent(name) + "/accept",
            { method: "POST" },
          );
        } else {
          res = await api(PROPOSED_SKILLS_PATH + encodeURIComponent(name), {
            method: "DELETE",
          });
        }
        if (!res.ok) {
          setState((await errorOf(res)) || what, true);
          return;
        }
        setProposalOpen((prev) => choose(prev?.name === name, null, prev));
        await fetchSkills();
      } catch {
        setState(what, true);
      }
    },
    [fetchSkills],
  );

  // restoreSkill brings an archived document back, and then RELOADS both lists: what moved
  // is a file, and the browser must not keep a guess about a directory it cannot see.
  const restoreSkill = useCallback(
    async (name: string) => {
      try {
        const res = await api(
          SKILLS_PATH + encodeURIComponent(name) + "/restore",
          { method: "POST" },
        );
        if (!res.ok) {
          setState((await errorOf(res)) || "could not restore the skill", true);
          return;
        }
        await fetchSkills();
      } catch {
        setState("could not restore the skill", true);
      }
    },
    [fetchSkills],
  );

  // disableSkill turns a skill off or back on. The list is patched locally: the answer is
  // one boolean and the row is already on screen, exactly like the pin.
  const disableSkill = useCallback(async (name: string, disabled: boolean) => {
    try {
      const res = await api(
        SKILLS_PATH + encodeURIComponent(name) + "/disable",
        {
          method: "POST",
          headers: JSON_HEADERS,
          body: JSON.stringify({ disabled }),
        },
      );
      if (!res.ok) {
        setState((await errorOf(res)) || "could not change the skill", true);
        return;
      }
      setSkills((prev) => prev.map((s) => patchNamed(s, name, { disabled })));
      setSkillOpen((prev) => prev && patchNamed(prev, name, { disabled }));
    } catch {
      setState("could not change the skill", true);
    }
  }, []);

  // deleteSkill removes the document for good, then RELOADS the list: a file left the
  // library and the browser must not keep a guess about a directory it cannot see. The
  // confirmation is asked before this is ever reached.
  const deleteSkill = useCallback(
    async (name: string) => {
      try {
        const res = await api(SKILLS_PATH + encodeURIComponent(name), {
          method: "DELETE",
        });
        if (!res.ok) {
          setState((await errorOf(res)) || "could not delete the skill", true);
          return;
        }
        setSkillOpen(null);
        await fetchSkills();
      } catch {
        setState("could not delete the skill", true);
      }
    },
    [fetchSkills],
  );

  // The library is fetched when the modal OPENS, and the draft state is reset with it so a
  // reopened modal never shows the previous document or the previous filter.
  useEffect(() => {
    if (!showSkillLibrary) {
      return;
    }
    setSkillQuery("");
    setSkillOpen(null);
    setSkillBody("");
    fetchSkills();
  }, [showSkillLibrary, fetchSkills]);

  // filteredSkills is what the list draws: the query filters by name, title and summary, in
  // memory, over the index already fetched. It is computed here and not in the JSX because
  // nothing in a render should be doing work the reader has to trace.
  const skillQueryNorm = skillQuery.trim().toLowerCase();
  const filteredSkills = filterSkills(skills, skillQueryNorm);

  const closeSkillLibrary = useCallback(() => setShowSkillLibrary(false), []);

  return {
    showSkillLibrary,
    setShowSkillLibrary,
    closeSkillLibrary,
    archivedSkills,
    proposedSkills,
    proposalOpen,
    skillQuery,
    setSkillQuery,
    skillQueryNorm,
    filteredSkills,
    skillOpen,
    setSkillOpen,
    skillBody,
    skillsBusy,
    openSkill,
    pinSkill,
    openProposal,
    decideProposal,
    restoreSkill,
    disableSkill,
    deleteSkill,
  };
}

export type Skills = ReturnType<typeof useSkills>;
